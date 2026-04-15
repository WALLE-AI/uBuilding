package prompt

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// User Context & System Context
// Maps to context.ts: getUserContext(), getSystemContext(), getGitStatus()
// ---------------------------------------------------------------------------

const maxGitStatusChars = 2000

// UserContext holds per-session user context data prepended to each conversation.
// Maps to the return type of getUserContext() in context.ts.
type UserContext struct {
	// ClaudeMd is the concatenated CLAUDE.md memory file content.
	ClaudeMd string `json:"claudeMd,omitempty"`

	// CurrentDate is today's date string ("Today's date is YYYY-MM-DD.").
	CurrentDate string `json:"currentDate"`
}

// SystemContext holds per-session system context appended to the system prompt.
// Maps to the return type of getSystemContext() in context.ts.
type SystemContext struct {
	// GitStatus is the git status snapshot at conversation start.
	GitStatus string `json:"gitStatus,omitempty"`
}

// ToMap converts UserContext to a string map for API injection.
func (u *UserContext) ToMap() map[string]string {
	m := make(map[string]string)
	if u.ClaudeMd != "" {
		m["claudeMd"] = u.ClaudeMd
	}
	if u.CurrentDate != "" {
		m["currentDate"] = u.CurrentDate
	}
	return m
}

// ToMap converts SystemContext to a string map for API injection.
func (s *SystemContext) ToMap() map[string]string {
	m := make(map[string]string)
	if s.GitStatus != "" {
		m["gitStatus"] = s.GitStatus
	}
	return m
}

// ---------------------------------------------------------------------------
// ContextProvider — memoized context fetcher
// ---------------------------------------------------------------------------

// ContextProvider computes and caches user/system context for a session.
// Context is computed once and memoized for the duration of the conversation.
// Maps to the memoize(getUserContext) and memoize(getSystemContext) in context.ts.
type ContextProvider struct {
	cwd              string
	claudeMdDirs     []string // additional directories for CLAUDE.md discovery
	disableClaudeMd  bool
	disableGit       bool

	userOnce    sync.Once
	userCtx     *UserContext
	systemOnce  sync.Once
	systemCtx   *SystemContext
}

// NewContextProvider creates a new context provider for the given working directory.
func NewContextProvider(cwd string, opts ...ContextProviderOption) *ContextProvider {
	cp := &ContextProvider{
		cwd: cwd,
	}
	for _, opt := range opts {
		opt(cp)
	}
	return cp
}

// ContextProviderOption configures a ContextProvider.
type ContextProviderOption func(*ContextProvider)

// WithClaudeMdDirs adds additional directories for CLAUDE.md discovery.
func WithClaudeMdDirs(dirs []string) ContextProviderOption {
	return func(cp *ContextProvider) {
		cp.claudeMdDirs = dirs
	}
}

// WithDisableClaudeMd disables CLAUDE.md loading.
func WithDisableClaudeMd(disable bool) ContextProviderOption {
	return func(cp *ContextProvider) {
		cp.disableClaudeMd = disable
	}
}

// WithDisableGit disables git status fetching.
func WithDisableGit(disable bool) ContextProviderOption {
	return func(cp *ContextProvider) {
		cp.disableGit = disable
	}
}

// GetUserContext returns the memoized user context.
// Maps to getUserContext() in context.ts.
func (cp *ContextProvider) GetUserContext() *UserContext {
	cp.userOnce.Do(func() {
		ctx := &UserContext{
			CurrentDate: fmt.Sprintf("Today's date is %s.", getLocalISODate()),
		}

		if !cp.disableClaudeMd {
			claudeMd := loadClaudeMdFiles(cp.cwd, cp.claudeMdDirs)
			if claudeMd != "" {
				ctx.ClaudeMd = claudeMd
			}
		}

		cp.userCtx = ctx
	})
	return cp.userCtx
}

// GetSystemContext returns the memoized system context.
// Maps to getSystemContext() in context.ts.
func (cp *ContextProvider) GetSystemContext() *SystemContext {
	cp.systemOnce.Do(func() {
		ctx := &SystemContext{}

		if !cp.disableGit {
			gitStatus := getGitStatus(cp.cwd)
			if gitStatus != "" {
				ctx.GitStatus = gitStatus
			}
		}

		cp.systemCtx = ctx
	})
	return cp.systemCtx
}

// Clear resets the memoized state so contexts are recomputed on next access.
func (cp *ContextProvider) Clear() {
	cp.userOnce = sync.Once{}
	cp.userCtx = nil
	cp.systemOnce = sync.Once{}
	cp.systemCtx = nil
}

// ---------------------------------------------------------------------------
// Git status — maps to getGitStatus() in context.ts
// ---------------------------------------------------------------------------

func getGitStatus(cwd string) string {
	if !isGitRepo(cwd) {
		return ""
	}

	type result struct {
		val string
		err error
	}

	branchCh := make(chan result, 1)
	mainBranchCh := make(chan result, 1)
	statusCh := make(chan result, 1)
	logCh := make(chan result, 1)
	userNameCh := make(chan result, 1)

	go func() {
		out, err := gitCmd(cwd, "rev-parse", "--abbrev-ref", "HEAD")
		branchCh <- result{out, err}
	}()
	go func() {
		out, err := getDefaultBranch(cwd)
		mainBranchCh <- result{out, err}
	}()
	go func() {
		out, err := gitCmd(cwd, "--no-optional-locks", "status", "--short")
		statusCh <- result{out, err}
	}()
	go func() {
		out, err := gitCmd(cwd, "--no-optional-locks", "log", "--oneline", "-n", "5")
		logCh <- result{out, err}
	}()
	go func() {
		out, err := gitCmd(cwd, "config", "user.name")
		userNameCh <- result{out, err}
	}()

	branch := (<-branchCh).val
	mainBranch := (<-mainBranchCh).val
	status := (<-statusCh).val
	log := (<-logCh).val
	userName := (<-userNameCh).val

	if status == "" {
		status = "(clean)"
	} else if len(status) > maxGitStatusChars {
		status = status[:maxGitStatusChars] + "\n... (truncated because it exceeds 2k characters. If you need more information, run \"git status\" using BashTool)"
	}

	parts := []string{
		`This is the git status at the start of the conversation. Note that this status is a snapshot in time, and will not update during the conversation.`,
		fmt.Sprintf("Current branch: %s", branch),
		fmt.Sprintf("Main branch (you will usually use this for PRs): %s", mainBranch),
	}
	if userName != "" {
		parts = append(parts, fmt.Sprintf("Git user: %s", userName))
	}
	parts = append(parts, fmt.Sprintf("Status:\n%s", status))
	parts = append(parts, fmt.Sprintf("Recent commits:\n%s", log))

	return strings.Join(parts, "\n\n")
}

func isGitRepo(cwd string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func getDefaultBranch(cwd string) (string, error) {
	// Try symbolic-ref first
	out, err := gitCmd(cwd, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil && out != "" {
		parts := strings.Split(out, "/")
		return parts[len(parts)-1], nil
	}
	// Fallback: check for common branch names
	for _, name := range []string{"main", "master"} {
		_, err := gitCmd(cwd, "rev-parse", "--verify", "refs/heads/"+name)
		if err == nil {
			return name, nil
		}
	}
	return "main", nil
}

func gitCmd(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ---------------------------------------------------------------------------
// CLAUDE.md loading — maps to getClaudeMds() + getMemoryFiles() in claudemd.ts
// ---------------------------------------------------------------------------

func loadClaudeMdFiles(cwd string, additionalDirs []string) string {
	var contents []string

	// Walk up from cwd to root looking for CLAUDE.md
	dirs := claudeMdSearchDirs(cwd)
	dirs = append(dirs, additionalDirs...)

	seen := make(map[string]bool)
	for _, dir := range dirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		if seen[absDir] {
			continue
		}
		seen[absDir] = true

		for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md"} {
			path := filepath.Join(absDir, name)
			data, err := os.ReadFile(path)
			if err == nil && len(data) > 0 {
				contents = append(contents, string(data))
			}
		}
	}

	if len(contents) == 0 {
		return ""
	}
	return strings.Join(contents, "\n\n---\n\n")
}

// claudeMdSearchDirs returns directories to search for CLAUDE.md,
// walking up from cwd to root.
func claudeMdSearchDirs(cwd string) []string {
	var dirs []string
	current := cwd
	for {
		dirs = append(dirs, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return dirs
}

// ---------------------------------------------------------------------------
// Date helpers — maps to common.ts
// ---------------------------------------------------------------------------

func getLocalISODate() string {
	if override := os.Getenv("CLAUDE_CODE_OVERRIDE_DATE"); override != "" {
		return override
	}
	now := time.Now()
	return fmt.Sprintf("%04d-%02d-%02d", now.Year(), int(now.Month()), now.Day())
}

// GetLocalMonthYear returns "Month YYYY" (e.g. "February 2026").
// Maps to getLocalMonthYear() in common.ts.
func GetLocalMonthYear() string {
	now := time.Now()
	if override := os.Getenv("CLAUDE_CODE_OVERRIDE_DATE"); override != "" {
		if t, err := time.Parse("2006-01-02", override); err == nil {
			now = t
		}
	}
	return now.Format("January 2006")
}
