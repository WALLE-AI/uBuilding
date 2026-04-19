// Package grep implements the Grep tool. It first tries an external ripgrep
// binary (via a pluggable Locator) and falls back to a Go regexp-based walker
// when rg is not available. The Locator interface makes both paths testable.
package grep

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/glob"
)

// Name is the tool name exposed to the model.
const Name = "Grep"

// Output modes mirror claude-code's Grep.
const (
	OutputContent          = "content"
	OutputFilesWithMatches = "files_with_matches"
	OutputCount            = "count"
)

const (
	// MaxMatches caps the total match lines returned.
	MaxMatches = 5000
	// MaxLineLen truncates individual match lines.
	MaxLineLen = 2000
)

// Input matches claude-code's Grep input (subset).
type Input struct {
	Pattern         string `json:"pattern"`
	Path            string `json:"path,omitempty"`
	Glob            string `json:"glob,omitempty"`
	CaseInsensitive bool   `json:"-i,omitempty"`
	ShowLineNumbers bool   `json:"-n,omitempty"`
	AfterContext    int    `json:"-A,omitempty"`
	BeforeContext   int    `json:"-B,omitempty"`
	Context         int    `json:"-C,omitempty"`
	OutputMode      string `json:"output_mode,omitempty"`
	HeadLimit       int    `json:"head_limit,omitempty"`
}

// MatchLine records a single matched line.
type MatchLine struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Text string `json:"text"`
}

// Output is the structured result.
type Output struct {
	Pattern    string         `json:"pattern"`
	Path       string         `json:"path"`
	OutputMode string         `json:"output_mode"`
	Files      []string       `json:"files,omitempty"`
	Counts     map[string]int `json:"counts,omitempty"`
	Matches    []MatchLine    `json:"matches,omitempty"`
	Total      int            `json:"total"`
	Truncated  bool           `json:"truncated,omitempty"`
	UsedRg     bool           `json:"used_rg,omitempty"`
}

// Locator finds an external ripgrep binary. Return ("", false) to force the
// Go fallback path (used in tests and in environments without rg).
type Locator interface {
	Locate() (string, bool)
}

// SystemLocator delegates to exec.LookPath("rg").
type SystemLocator struct{}

func (SystemLocator) Locate() (string, bool) {
	p, err := exec.LookPath("rg")
	if err != nil {
		return "", false
	}
	return p, true
}

// NoopLocator always forces the Go fallback.
type NoopLocator struct{}

func (NoopLocator) Locate() (string, bool) { return "", false }

// Tool implements tool.Tool.
type Tool struct {
	tool.ToolDefaults
	locator Locator
}

// New returns a Grep tool using the system rg when available.
func New() *Tool { return &Tool{locator: SystemLocator{}} }

// WithLocator overrides the ripgrep locator (used for tests).
func (t *Tool) WithLocator(l Locator) *Tool { t.locator = l; return t }

func (t *Tool) Name() string                             { return Name }
func (t *Tool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *Tool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (t *Tool) MaxResultSizeChars() int                  { return 100_000 }

func (t *Tool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"pattern":     {Type: "string", Description: "Regex to search for."},
			"path":        {Type: "string", Description: "Root directory (default: cwd)."},
			"glob":        {Type: "string", Description: "Restrict to files matching this glob."},
			"-i":          {Type: "boolean", Description: "Case-insensitive search."},
			"-n":          {Type: "boolean", Description: "Include line numbers."},
			"-A":          {Type: "integer", Description: "Lines of trailing context."},
			"-B":          {Type: "integer", Description: "Lines of leading context."},
			"-C":          {Type: "integer", Description: "Lines of surrounding context (overrides -A/-B)."},
			"output_mode": {Type: "string", Description: "content | files_with_matches | count", Enum: []string{"content", "files_with_matches", "count"}},
			"head_limit":  {Type: "integer", Description: "Max result rows (applies to content/files modes)."},
		},
		Required: []string{"pattern"},
	}
}

func (t *Tool) Description(input json.RawMessage) string {
	var in Input
	_ = json.Unmarshal(input, &in)
	if in.Pattern == "" {
		return "Grep files"
	}
	return "Grep " + in.Pattern
}

func (t *Tool) Prompt(opts tool.PromptOptions) string {
	bashRef := resolvePeer(opts, "Bash")
	agentRef := resolvePeer(opts, "Task")
	return `A powerful search tool built on ripgrep

Usage:
- ALWAYS use ` + Name + ` for search tasks. NEVER invoke ` + "`grep`" + ` or ` + "`rg`" + ` as a ` + bashRef + ` command. The ` + Name + ` tool has been optimized for correct permissions and access.
- Supports full regex syntax (e.g., "log.*Error", "function\s+\w+")
- Filter files with the glob parameter (e.g., "*.js", "**/*.tsx").
- Output modes: "content" (default — matching lines), "files_with_matches" (file paths only), "count" (per-file match counts).
- Use the ` + agentRef + ` tool for open-ended searches requiring multiple rounds.
- Pattern syntax: Uses ripgrep (not grep) — literal braces need escaping (use ` + "`interface\\{\\}`" + ` to find ` + "`interface{}`" + ` in Go code).
- Multiline matching: By default patterns match within single lines only. For cross-line patterns like ` + "`struct \\{[\\s\\S]*?field`" + `, use a multi-line-aware regex and set -C to capture surrounding context.
- Context flags: -A (trailing), -B (leading), -C (both, overrides -A/-B). -n toggles 1-indexed line numbers in output.
- head_limit caps the result rows (applies to content & files_with_matches modes). The engine transparently falls back to a Go regex walker when ripgrep is not on PATH.`
}

func (t *Tool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return &tool.ValidationResult{Valid: false, Message: "pattern must not be empty"}
	}
	if _, err := regexp.Compile(in.Pattern); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid regex: %v", err)}
	}
	if in.OutputMode != "" &&
		in.OutputMode != OutputContent &&
		in.OutputMode != OutputFilesWithMatches &&
		in.OutputMode != OutputCount {
		return &tool.ValidationResult{Valid: false, Message: "output_mode must be content|files_with_matches|count"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *Tool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "grep-is-safe"}, nil
}

func (t *Tool) Call(ctx context.Context, input json.RawMessage, _ *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	root := in.Path
	if root == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = cwd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	mode := in.OutputMode
	if mode == "" {
		mode = OutputContent
	}

	var out *Output
	if rg, ok := t.locator.Locate(); ok {
		res, runErr := runRipgrep(ctx, rg, abs, in, mode)
		if runErr == nil {
			out = res
			out.UsedRg = true
		}
		// Fall through to Go fallback on rg error.
	}
	if out == nil {
		res, ferr := runGoFallback(ctx, abs, in, mode)
		if ferr != nil {
			return nil, ferr
		}
		out = res
	}
	out.Pattern = in.Pattern
	out.Path = abs
	out.OutputMode = mode
	return &tool.ToolResult{Data: *out}, nil
}

func (t *Tool) MapToolResultToParam(content interface{}, toolUseID string) *agents.ContentBlock {
	return &agents.ContentBlock{
		Type:      agents.ContentBlockToolResult,
		ToolUseID: toolUseID,
		Content:   renderOutput(content),
	}
}

func renderOutput(content interface{}) string {
	var out Output
	switch v := content.(type) {
	case Output:
		out = v
	case *Output:
		if v != nil {
			out = *v
		}
	case string:
		return v
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
	var sb strings.Builder
	switch out.OutputMode {
	case OutputFilesWithMatches:
		if len(out.Files) == 0 {
			return fmt.Sprintf("No files match %q under %s", out.Pattern, out.Path)
		}
		fmt.Fprintf(&sb, "Files with matches for %q under %s:\n", out.Pattern, out.Path)
		for _, f := range out.Files {
			sb.WriteString(f)
			sb.WriteByte('\n')
		}
	case OutputCount:
		if len(out.Counts) == 0 {
			return fmt.Sprintf("No matches for %q under %s", out.Pattern, out.Path)
		}
		fmt.Fprintf(&sb, "Match counts for %q under %s:\n", out.Pattern, out.Path)
		keys := make([]string, 0, len(out.Counts))
		for k := range out.Counts {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&sb, "%s: %d\n", k, out.Counts[k])
		}
	default:
		if len(out.Matches) == 0 {
			return fmt.Sprintf("No matches for %q under %s", out.Pattern, out.Path)
		}
		fmt.Fprintf(&sb, "Matches for %q under %s (%d):\n", out.Pattern, out.Path, out.Total)
		for _, m := range out.Matches {
			if m.Line > 0 {
				fmt.Fprintf(&sb, "%s:%d: %s\n", m.Path, m.Line, m.Text)
			} else {
				fmt.Fprintf(&sb, "%s: %s\n", m.Path, m.Text)
			}
		}
	}
	if out.Truncated {
		sb.WriteString("… (truncated)\n")
	}
	return sb.String()
}

// ── Go fallback ────────────────────────────────────────────────────────────

func runGoFallback(ctx context.Context, root string, in Input, mode string) (*Output, error) {
	pat := in.Pattern
	if in.CaseInsensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return nil, err
	}
	globSegs, err := globCompile(in.Glob)
	if err != nil {
		return nil, err
	}
	out := &Output{Counts: map[string]int{}}
	head := in.HeadLimit
	if head <= 0 {
		head = MaxMatches
	}
	before := in.BeforeContext
	after := in.AfterContext
	if in.Context > 0 {
		before = in.Context
		after = in.Context
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			// Skip noisy dirs by convention.
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" {
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if globSegs != nil {
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if !globMatch(rel, globSegs) {
				return nil
			}
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if looksBinary(data) {
			return nil
		}
		lines := bytes.Split(data, []byte("\n"))
		var matchedIdx []int
		for i, line := range lines {
			if re.Match(line) {
				matchedIdx = append(matchedIdx, i)
			}
		}
		if len(matchedIdx) == 0 {
			return nil
		}
		if mode == OutputFilesWithMatches {
			out.Files = append(out.Files, path)
			return nil
		}
		if mode == OutputCount {
			out.Counts[path] = len(matchedIdx)
			out.Total += len(matchedIdx)
			return nil
		}
		// Content mode — emit with context.
		for _, mi := range matchedIdx {
			lo := mi - before
			if lo < 0 {
				lo = 0
			}
			hi := mi + after
			if hi >= len(lines) {
				hi = len(lines) - 1
			}
			for li := lo; li <= hi; li++ {
				text := string(lines[li])
				if len(text) > MaxLineLen {
					text = text[:MaxLineLen] + "…"
				}
				ml := MatchLine{Path: path, Text: text}
				if in.ShowLineNumbers || before+after > 0 {
					ml.Line = li + 1
				}
				out.Matches = append(out.Matches, ml)
				out.Total++
				if out.Total >= head {
					out.Truncated = true
					return errStop
				}
			}
		}
		return nil
	})
	if err != nil && err != errStop {
		return nil, err
	}
	if mode == OutputFilesWithMatches {
		sort.Strings(out.Files)
		out.Total = len(out.Files)
		if head > 0 && len(out.Files) > head {
			out.Files = out.Files[:head]
			out.Truncated = true
		}
	}
	return out, nil
}

var errStop = fmt.Errorf("grep: head limit reached")

func looksBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

// ── glob helper (lightweight reuse of the glob package) ───────────────────

func globCompile(pattern string) ([]globSeg, error) {
	if pattern == "" {
		return nil, nil
	}
	parts := strings.Split(filepath.ToSlash(pattern), "/")
	segs := make([]globSeg, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		if p == "**" {
			segs = append(segs, globSeg{doubleStar: true})
			continue
		}
		if _, err := filepath.Match(p, p); err != nil {
			return nil, err
		}
		segs = append(segs, globSeg{raw: p})
	}
	return segs, nil
}

type globSeg struct {
	raw        string
	doubleStar bool
}

func globMatch(path string, segs []globSeg) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	return globDP(parts, 0, segs, 0)
}

func globDP(parts []string, pi int, segs []globSeg, si int) bool {
	for si < len(segs) && pi <= len(parts) {
		s := segs[si]
		if s.doubleStar {
			for k := pi; k <= len(parts); k++ {
				if globDP(parts, k, segs, si+1) {
					return true
				}
			}
			return false
		}
		if pi >= len(parts) {
			return false
		}
		ok, _ := filepath.Match(s.raw, parts[pi])
		if !ok {
			return false
		}
		si++
		pi++
	}
	for si < len(segs) && segs[si].doubleStar {
		si++
	}
	return si == len(segs) && pi == len(parts)
}

// ── ripgrep fast path ──────────────────────────────────────────────────────

func runRipgrep(ctx context.Context, rgPath, root string, in Input, mode string) (*Output, error) {
	args := []string{"--json", "-S"}
	if in.CaseInsensitive {
		args = append(args, "-i")
	}
	if in.Glob != "" {
		args = append(args, "-g", in.Glob)
	}
	before := in.BeforeContext
	after := in.AfterContext
	if in.Context > 0 {
		before = in.Context
		after = in.Context
	}
	if before > 0 {
		args = append(args, "-B", fmt.Sprintf("%d", before))
	}
	if after > 0 {
		args = append(args, "-A", fmt.Sprintf("%d", after))
	}
	switch mode {
	case OutputFilesWithMatches:
		args = append(args, "-l")
	case OutputCount:
		args = append(args, "-c")
	}
	args = append(args, "--", in.Pattern, root)

	cmd := exec.CommandContext(ctx, rgPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer cmd.Wait()

	out := &Output{Counts: map[string]int{}}
	head := in.HeadLimit
	if head <= 0 {
		head = MaxMatches
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var evt struct {
			Type string `json:"type"`
			Data struct {
				Path struct {
					Text string `json:"text"`
				} `json:"path"`
				Lines struct {
					Text string `json:"text"`
				} `json:"lines"`
				LineNumber int `json:"line_number"`
				Stats      struct {
					MatchedLines int `json:"matched_lines"`
				} `json:"stats"`
			} `json:"data"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
			continue
		}
		switch evt.Type {
		case "match", "context":
			path := evt.Data.Path.Text
			text := strings.TrimRight(evt.Data.Lines.Text, "\n")
			if len(text) > MaxLineLen {
				text = text[:MaxLineLen] + "…"
			}
			if mode == OutputContent {
				out.Matches = append(out.Matches, MatchLine{Path: path, Line: evt.Data.LineNumber, Text: text})
				out.Total++
				if out.Total >= head {
					out.Truncated = true
					_ = cmd.Process.Kill()
					return out, nil
				}
			}
		case "end":
			path := evt.Data.Path.Text
			if mode == OutputFilesWithMatches {
				out.Files = append(out.Files, path)
				out.Total++
			} else if mode == OutputCount {
				out.Counts[path] = evt.Data.Stats.MatchedLines
				out.Total += evt.Data.Stats.MatchedLines
			}
		}
	}
	return out, nil
}

var _ tool.Tool = (*Tool)(nil)

// Re-export the glob Output type for callers that want to chain through to
// a glob-style listing. Unused at runtime but prevents dead-code lint when
// the glob package is imported for compile-time alignment.
var _ = glob.MaxResults
