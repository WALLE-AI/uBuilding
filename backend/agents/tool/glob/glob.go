// Package glob implements the Glob tool. It performs fast pattern matching
// (including ** recursion) over a filesystem subtree and returns results
// sorted by modification time (newest first). Written on top of the standard
// library; no external dependencies.
package glob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wall-ai/ubuilding/backend/agents"
	"github.com/wall-ai/ubuilding/backend/agents/tool"
	"github.com/wall-ai/ubuilding/backend/agents/tool/cwd"
)

// Name is the tool name exposed to the model.
const Name = "Glob"

// MaxResults caps the number of matches returned.
const MaxResults = 1000

// Input matches claude-code's Glob input.
type Input struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

// Match represents a single glob hit.
type Match struct {
	Path    string `json:"path"`
	ModTime int64  `json:"mtime"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"is_dir,omitempty"`
}

// Output is the structured result.
type Output struct {
	Pattern   string  `json:"pattern"`
	Path      string  `json:"path"`
	Matches   []Match `json:"matches"`
	Count     int     `json:"count"`
	Truncated bool    `json:"truncated,omitempty"`
}

// Tool implements tool.Tool.
type Tool struct {
	tool.ToolDefaults
}

// New returns a Glob tool.
func New() *Tool { return &Tool{} }

func (t *Tool) Name() string                             { return Name }
func (t *Tool) IsReadOnly(_ json.RawMessage) bool        { return true }
func (t *Tool) IsConcurrencySafe(_ json.RawMessage) bool { return true }
func (t *Tool) MaxResultSizeChars() int                  { return 100_000 }

func (t *Tool) InputSchema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]*tool.SchemaProperty{
			"pattern": {Type: "string", Description: "Glob pattern (supports **, *, ?, [])."},
			"path":    {Type: "string", Description: "Root directory to search (default: cwd)."},
		},
		Required: []string{"pattern"},
	}
}

func (t *Tool) Description(input json.RawMessage) string {
	var in Input
	_ = json.Unmarshal(input, &in)
	if in.Pattern == "" {
		return "Glob files"
	}
	return "Glob " + in.Pattern
}

func (t *Tool) Prompt(opts tool.PromptOptions) string {
	agentRef := resolvePeer(opts, "Task")
	return `- Fast file pattern matching tool that works with any codebase size
- Supports glob patterns like "**/*.js" or "src/**/*.ts"
- Returns matching file paths sorted by modification time (newest first)
- Use this tool when you need to find files by name patterns
- When you are doing an open ended search that may require multiple rounds of globbing and grepping, use the ` + agentRef + ` tool instead
- path defaults to the engine's working directory; pattern must not be empty
- Returns at most ` + fmt.Sprintf("%d", MaxResults) + ` matches; refine the pattern if truncated`
}

func (t *Tool) ValidateInput(input json.RawMessage, _ *agents.ToolUseContext) *tool.ValidationResult {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return &tool.ValidationResult{Valid: false, Message: fmt.Sprintf("invalid input: %v", err)}
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return &tool.ValidationResult{Valid: false, Message: "pattern must not be empty"}
	}
	return &tool.ValidationResult{Valid: true}
}

func (t *Tool) CheckPermissions(input json.RawMessage, _ *agents.ToolUseContext) (*tool.PermissionResult, error) {
	return &tool.PermissionResult{Behavior: tool.PermissionAllow, UpdatedInput: input, DecisionReason: "glob-is-safe"}, nil
}

func (t *Tool) Call(ctx context.Context, input json.RawMessage, _ *agents.ToolUseContext) (*tool.ToolResult, error) {
	var in Input
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("invalid input: %w", err)
	}
	root := in.Path
	if root == "" {
		root = cwd.Get()
		if root == "" {
			root, _ = os.Getwd()
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	matches, truncated, err := walkMatch(ctx, abs, in.Pattern)
	if err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ModTime > matches[j].ModTime })
	return &tool.ToolResult{Data: Output{
		Pattern:   in.Pattern,
		Path:      abs,
		Matches:   matches,
		Count:     len(matches),
		Truncated: truncated,
	}}, nil
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
	if len(out.Matches) == 0 {
		return fmt.Sprintf("No matches for %q under %s", out.Pattern, out.Path)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Found %d match(es) for %q under %s:\n", out.Count, out.Pattern, out.Path)
	for _, m := range out.Matches {
		sb.WriteString(m.Path)
		sb.WriteByte('\n')
	}
	if out.Truncated {
		sb.WriteString("… (truncated)\n")
	}
	return sb.String()
}

// walkMatch walks root honoring ctx cancellation and returns matches that
// satisfy pattern. A simple implementation: compile the pattern into a
// per-segment matcher tree, then walk.
func walkMatch(ctx context.Context, root, pattern string) ([]Match, bool, error) {
	segs, err := compilePattern(pattern)
	if err != nil {
		return nil, false, err
	}
	var results []Match
	truncated := false
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			// Skip unreadable dirs silently.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if path == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if matchSegments(rel, segs) {
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			results = append(results, Match{
				Path:    path,
				ModTime: info.ModTime().UnixNano(),
				Size:    info.Size(),
				IsDir:   d.IsDir(),
			})
			if len(results) >= MaxResults {
				truncated = true
				return errStopWalk
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopWalk) {
		return nil, false, err
	}
	return results, truncated, nil
}

var errStopWalk = errors.New("glob: max results reached")

// compilePattern turns a glob string into a sequence of segment matchers.
// "**" matches zero or more path segments; other segments are matched via
// path.Match on individual components.
type segMatcher struct {
	raw        string
	doubleStar bool
}

func compilePattern(pattern string) ([]segMatcher, error) {
	pattern = filepath.ToSlash(pattern)
	parts := strings.Split(pattern, "/")
	out := make([]segMatcher, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		if p == "**" {
			out = append(out, segMatcher{doubleStar: true})
			continue
		}
		// Validate via filepath.Match on a dummy string to catch pattern errors.
		if _, err := filepath.Match(p, p); err != nil {
			return nil, err
		}
		out = append(out, segMatcher{raw: p})
	}
	return out, nil
}

func matchSegments(path string, segs []segMatcher) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	return matchDP(parts, 0, segs, 0)
}

// matchDP performs a recursive match with memoisation-free backtracking; path
// trees are shallow enough that this is fine in practice.
func matchDP(parts []string, pi int, segs []segMatcher, si int) bool {
	for si < len(segs) && pi <= len(parts) {
		s := segs[si]
		if s.doubleStar {
			// Try consuming 0..N segments.
			for k := pi; k <= len(parts); k++ {
				if matchDP(parts, k, segs, si+1) {
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
	// Consume trailing ** gracefully.
	for si < len(segs) && segs[si].doubleStar {
		si++
	}
	return si == len(segs) && pi == len(parts)
}

var _ tool.Tool = (*Tool)(nil)
