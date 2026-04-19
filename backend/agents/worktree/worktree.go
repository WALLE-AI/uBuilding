// Package worktree — git worktree management for isolated sub-agents.
//
// Task D15 · port src/services/worktree/agentWorktree.ts. When an agent
// definition declares `Isolation = "worktree"` the host creates a
// dedicated git worktree so the sub-agent's edits don't stomp on the
// parent session. This package provides the minimal surface:
//
//   - CreateAgentWorktree(repoRoot, slug) → (path, err)
//   - HasWorktreeChanges(path) → (bool, err)
//   - RemoveAgentWorktree(path) → err
//   - RunWithCwdOverride(ctx, cwd, fn) → err
//
// Implementation uses shell-outs to `git worktree` so we don't depend on
// go-git (which has spotty Windows behaviour on large repos). Tests
// gracefully t.Skip when git isn't on PATH.
//
// Worktree paths are placed under `<repoRoot>/.worktrees/<slug>` so git
// handles cleanup via the standard prune flow. The slug is sanitised for
// filesystem safety on Windows.
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Git availability
// ---------------------------------------------------------------------------

// ErrGitUnavailable is returned when `git` cannot be located on PATH.
var ErrGitUnavailable = errors.New("worktree: git binary not available on PATH")

// Available reports whether `git` is on PATH. Cached per-process.
func Available() bool { return gitPath() != "" }

var (
	gitPathOnce sync.Once
	gitPathVal  string
)

func gitPath() string {
	gitPathOnce.Do(func() {
		if p, err := exec.LookPath("git"); err == nil {
			gitPathVal = p
		}
	})
	return gitPathVal
}

// ---------------------------------------------------------------------------
// Slug sanitisation
// ---------------------------------------------------------------------------

// sanitizeSlug turns a free-form agent slug into a filesystem-safe token.
// Windows is the strict case; we drop every character outside
// `[a-zA-Z0-9_-]` and collapse consecutive dashes.
func sanitizeSlug(slug string) string {
	trimmed := strings.TrimSpace(slug)
	if trimmed == "" {
		return "agent"
	}
	var b strings.Builder
	prevDash := false
	for _, r := range trimmed {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "agent"
	}
	return out
}

// worktreeBranchName returns the ephemeral branch name for a worktree.
// The timestamp suffix keeps collisions unlikely when the same slug is
// reused back-to-back.
func worktreeBranchName(slug string) string {
	return fmt.Sprintf("ubuilding/%s-%d", sanitizeSlug(slug), time.Now().UnixNano())
}

// ---------------------------------------------------------------------------
// Public API
// ---------------------------------------------------------------------------

// CreateAgentWorktree creates a new git worktree under
// `<repoRoot>/.worktrees/<slug>-<timestamp>` on a fresh branch forked
// from HEAD. Returns the worktree path on success. The caller is
// responsible for calling RemoveAgentWorktree when the agent terminates.
func CreateAgentWorktree(ctx context.Context, repoRoot, slug string) (string, error) {
	if gitPath() == "" {
		return "", ErrGitUnavailable
	}
	if strings.TrimSpace(repoRoot) == "" {
		return "", errors.New("worktree: repoRoot required")
	}
	cleanSlug := sanitizeSlug(slug)
	stamp := time.Now().UnixNano()
	dir := filepath.Join(repoRoot, ".worktrees", fmt.Sprintf("%s-%d", cleanSlug, stamp))
	branch := worktreeBranchName(cleanSlug)

	// git worktree add -b <branch> <dir>
	if _, err := runGit(ctx, repoRoot, "worktree", "add", "-b", branch, dir); err != nil {
		return "", fmt.Errorf("worktree: create %s: %w", dir, err)
	}
	return dir, nil
}

// HasWorktreeChanges reports whether the worktree at path has uncommitted
// changes (staged OR unstaged). A dirty worktree is the caller's signal
// that the sub-agent produced work worth preserving before removal.
func HasWorktreeChanges(ctx context.Context, path string) (bool, error) {
	if gitPath() == "" {
		return false, ErrGitUnavailable
	}
	out, err := runGit(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("worktree: status %s: %w", path, err)
	}
	return strings.TrimSpace(out) != "", nil
}

// RemoveAgentWorktree tears down the worktree at path. Uses `--force` so
// dirty worktrees don't block the teardown; callers that care about dirty
// state should gate on HasWorktreeChanges first.
func RemoveAgentWorktree(ctx context.Context, path string) error {
	if gitPath() == "" {
		return ErrGitUnavailable
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("worktree: path required")
	}
	// `git worktree remove` must be invoked from the parent repo, not
	// from inside the worktree itself. We derive the parent by walking
	// up from path/.. until we find `.git` pointing to a gitdir.
	parent := findParentRepo(path)
	if parent == "" {
		// Fallback: try running from path itself. Some git versions accept it.
		parent = path
	}
	if _, err := runGit(ctx, parent, "worktree", "remove", "--force", path); err != nil {
		return fmt.Errorf("worktree: remove %s: %w", path, err)
	}
	return nil
}

// findParentRepo walks up from path looking for a .git directory that
// isn't the worktree's own gitdir file. Returns "" when nothing matches.
func findParentRepo(path string) string {
	cur := filepath.Dir(path) // start above the worktree
	for i := 0; i < 32; i++ { // cap loop to avoid infinite walks
		if cur == "" || cur == string(filepath.Separator) || cur == "." {
			return ""
		}
		if info, err := os.Stat(filepath.Join(cur, ".git")); err == nil && info.IsDir() {
			return cur
		}
		next := filepath.Dir(cur)
		if next == cur {
			return ""
		}
		cur = next
	}
	return ""
}

// ---------------------------------------------------------------------------
// RunWithCwdOverride
// ---------------------------------------------------------------------------

// cwdOverrideMu serialises calls to os.Chdir to avoid the racing two
// goroutines both swapping cwd mid-flight.
var cwdOverrideMu sync.Mutex

// RunWithCwdOverride temporarily changes the process cwd to `cwd`, runs
// fn, and restores the original cwd. Returns whatever fn returns. Safe
// for concurrent callers (serialised via a package-level mutex).
//
// NOTE: callers that care about concurrency-free operation should pass
// Cwd explicitly to their sub-commands instead (e.g. `exec.Command` with
// `Dir` set). RunWithCwdOverride is intended for legacy call sites that
// assume `os.Getwd()` as their root.
func RunWithCwdOverride(ctx context.Context, cwd string, fn func(ctx context.Context) error) error {
	if strings.TrimSpace(cwd) == "" {
		return errors.New("worktree: cwd required")
	}
	cwdOverrideMu.Lock()
	defer cwdOverrideMu.Unlock()

	orig, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("worktree: getwd: %w", err)
	}
	if err := os.Chdir(cwd); err != nil {
		return fmt.Errorf("worktree: chdir %s: %w", cwd, err)
	}
	// Best-effort restore even if fn panics.
	defer func() {
		_ = os.Chdir(orig)
	}()
	return fn(ctx)
}

// ---------------------------------------------------------------------------
// Internal · git subprocess helper
// ---------------------------------------------------------------------------

// runGit shells out to git in repoDir and returns stdout. Stderr is
// captured and folded into the returned error on failure.
func runGit(ctx context.Context, repoDir string, args ...string) (string, error) {
	bin := gitPath()
	if bin == "" {
		return "", ErrGitUnavailable
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}
