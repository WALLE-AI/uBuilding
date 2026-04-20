package memory

import (
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// M3.I1-I5 · Parsing primitives for CLAUDE.md files.
//
//   - ParseFrontmatter            (M3.I1)
//   - quoteProblematicValues      (M3.I2, internal helper used by retry)
//   - StripHtmlComments           (M3.I3)
//   - ExtractIncludePaths         (M3.I4)
//   - SplitPathInFrontmatter      (M3.I5)
//
// Pure functions — no filesystem access. Designed for table-driven
// tests under claudemd_parse_test.go.
// ---------------------------------------------------------------------------

// FrontmatterData holds the tiny subset of YAML keys the memory
// loader actually inspects. Unknown keys are still parsed but end up
// in the Extras map so hosts can read them without a second pass.
//
// Mirrors `FrontmatterData` in TS `src/utils/frontmatterParser.ts`
// but trimmed down — the memory subsystem only cares about `paths:`
// and `type:`; the rest of the TS union (allowed-tools, shell, etc.)
// is the concern of skills/commands, not CLAUDE.md.
type FrontmatterData struct {
	// Paths is the raw `paths:` field. Supports both a comma-separated
	// string and a YAML list. Callers should run SplitPathInFrontmatter
	// on this value to get the expanded glob list.
	Paths interface{} `yaml:"paths,omitempty"`

	// Description is the optional `description:` field used by memory
	// files to provide a short summary for the recall selector.
	// Mirrors TS `FrontmatterData.description`.
	Description string `yaml:"description,omitempty"`

	// Type is the optional `type:` declaration used by AutoMem entries
	// to tag individual MEMORY.md sections. Accepted values are
	// user/feedback/project/reference; see ParseMemoryTypeFrontmatter.
	Type string `yaml:"type,omitempty"`

	// Extras captures every other top-level key so downstream code can
	// look them up without having to re-parse the YAML. The yaml tag
	// must stay `",inline"` so keys fall through.
	Extras map[string]interface{} `yaml:",inline"`
}

// ParsedMarkdown is the return type of ParseFrontmatter. Matches the
// TS `ParsedMarkdown` shape (frontmatter + body).
type ParsedMarkdown struct {
	Frontmatter FrontmatterData
	// Content is the markdown body AFTER the frontmatter fences. When
	// no frontmatter block is present, Content equals the input verbatim.
	Content string
}

// Regex for the opening frontmatter fence. Accepts `---` optionally
// followed by whitespace and then a newline. The content body starts
// after the closing fence.
//
// Pre-compiled as package-level regex for performance since memory
// files are re-parsed frequently.
var frontmatterRe = regexp.MustCompile(`(?s)\A---[ \t]*\n(.*?)\n---[ \t]*(?:\n|$)`)

// ---------------------------------------------------------------------------
// M3.I1 · ParseFrontmatter
// ---------------------------------------------------------------------------

// ParseFrontmatter extracts a YAML frontmatter block from raw and
// returns the parsed structure plus the remaining markdown body.
// The function is forgiving:
//
//  1. No fence at all         → empty FrontmatterData + raw body.
//  2. Well-formed YAML        → parsed.
//  3. Malformed YAML that
//     would trip yaml.v3 on
//     glob characters         → retried via quoteProblematicValues.
//  4. Retry still fails       → empty FrontmatterData + residual body.
//
// Callers that care about the failure path can inspect
// returnValue.Frontmatter.Extras — it will be nil when no frontmatter
// was parsed.
func ParseFrontmatter(raw string) ParsedMarkdown {
	match := frontmatterRe.FindStringSubmatchIndex(raw)
	if match == nil {
		return ParsedMarkdown{Content: raw}
	}

	frontmatterText := raw[match[2]:match[3]]
	body := raw[match[1]:]

	var data FrontmatterData
	if err := yaml.Unmarshal([]byte(frontmatterText), &data); err != nil {
		quoted := quoteProblematicValues(frontmatterText)
		if err2 := yaml.Unmarshal([]byte(quoted), &data); err2 != nil {
			// Both attempts failed: log-equivalent behaviour is to
			// return an empty FrontmatterData; callers treat this as
			// "no frontmatter present".
			return ParsedMarkdown{Content: body}
		}
	}

	return ParsedMarkdown{Frontmatter: data, Content: body}
}

// ---------------------------------------------------------------------------
// M3.I2 · quoteProblematicValues
// ---------------------------------------------------------------------------

// yamlSpecialChars mirrors the TS YAML_SPECIAL_CHARS regex. Matches
// characters that require quoting when they appear as the value half
// of a simple `key: value` line:
//
//   - { } [ ] flow indicators
//   - * &            anchor / alias
//   - # ! | > %      comment / tag / block scalar / directive
//   - @ `            reserved
//   - `: ` (colon + space) — trips nested-mapping parser error.
//
// Glob patterns like `src/*.{ts,tsx}` hit this list on `{` and `*`.
var yamlSpecialChars = regexp.MustCompile("[{}\\[\\]*&#!|>%@`]|: ")

// keyValueLineRe matches `key: value` lines at column 0 (no
// indentation, no list dash). Groups: key, value.
var keyValueLineRe = regexp.MustCompile(`^([a-zA-Z_-]+):[ \t]+(.+)$`)

// quoteProblematicValues scans `raw` line by line and wraps any
// right-hand side containing YAML-special characters in double quotes
// (escaping existing quotes + backslashes first). Unchanged lines are
// preserved byte-identically so diffs stay minimal.
func quoteProblematicValues(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		m := keyValueLineRe.FindStringSubmatch(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		key, val := m[1], m[2]
		// Leave already-quoted values alone.
		if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
			(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
			out = append(out, line)
			continue
		}
		if !yamlSpecialChars.MatchString(val) {
			out = append(out, line)
			continue
		}
		escaped := strings.ReplaceAll(val, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		out = append(out, key+`: "`+escaped+`"`)
	}
	return strings.Join(out, "\n")
}

// ---------------------------------------------------------------------------
// M3.I3 · StripHtmlComments
// ---------------------------------------------------------------------------

// htmlCommentRe matches a well-formed `<!-- ... -->` span. `(?s)`
// enables dot-matches-newline so comments can span lines.
var htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

// codeFenceOpenRe recognises an opening ``` / ~~~ fence at the start
// of a line (allowing leading whitespace). The captured marker is
// used to find the matching closer.
var codeFenceOpenRe = regexp.MustCompile("(?m)^([ \\t]*)(`{3,}|~{3,})(.*)$")

// StripHtmlComments removes block-level HTML comment spans from
// content while preserving:
//
//   - `<!-- ... -->` inside fenced code blocks (``` or ~~~)
//   - unclosed `<!--` (kept verbatim so a stray sequence does not
//     silently swallow the rest of the file)
//
// Inline comments embedded in a paragraph are treated the same as
// block comments — Go has no markdown lexer here, so we err on the
// side of stripping more aggressively than TS. Callers that rely on
// inline-comment preservation should author with fenced code blocks.
//
// Returns (newContent, stripped) where stripped=true iff any bytes
// were removed.
func StripHtmlComments(content string) (string, bool) {
	if !strings.Contains(content, "<!--") {
		return content, false
	}
	// Walk the content line-by-line while tracking fence state. Build
	// a list of (segmentText, insideFence) runs so we can apply the
	// comment regex only to non-fence runs.
	lines := strings.SplitAfter(content, "\n")

	var b strings.Builder
	b.Grow(len(content))
	stripped := false

	inFence := false
	var fenceMarker string
	// currentNonFence buffers the contiguous non-fence region that we
	// flush together once a fence opens (or at EOF). Flushing in bulk
	// lets the regex find comments that span multiple lines without a
	// fence in between.
	var nonFence strings.Builder

	flush := func() {
		if nonFence.Len() == 0 {
			return
		}
		text := nonFence.String()
		nonFence.Reset()
		// Only strip comments that start at column 0 (possibly
		// preceded by whitespace) to approximate CommonMark's block-
		// level rule. Paragraph-embedded `<!--` inside a sentence
		// stays put.
		stripTextBlockLevel(text, &b, &stripped)
	}

	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if inFence {
			b.WriteString(line)
			// Close when the line's stripped prefix equals the marker
			// repeated. Upstream's lexer handles ≥marker fences; we
			// approximate with HasPrefix + "line contains nothing but
			// the marker + optional trailing whitespace".
			if isFenceClose(trimmed, fenceMarker) {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if m := codeFenceOpenRe.FindStringSubmatch(line); m != nil {
			flush()
			b.WriteString(line)
			fenceMarker = m[2]
			inFence = true
			continue
		}
		nonFence.WriteString(line)
	}
	flush()

	return b.String(), stripped
}

// isFenceClose reports whether a trimmed line closes a fence opened
// with `marker`. A closer is a run of at least len(marker) of the
// same fence character (` or ~), optionally followed by whitespace.
func isFenceClose(trimmed, marker string) bool {
	if marker == "" || !strings.HasPrefix(trimmed, marker) {
		return false
	}
	ch := marker[0]
	i := 0
	for i < len(trimmed) && trimmed[i] == ch {
		i++
	}
	if i < len(marker) {
		return false
	}
	rest := strings.TrimRight(trimmed[i:], " \t\r\n")
	return rest == ""
}

// stripTextBlockLevel walks through non-fence text finding lines that
// start with `<!--` (block-level) and removing the containing comment
// span. Inline `<!--` mid-paragraph is left alone so the regex never
// gobbles across unrelated content.
func stripTextBlockLevel(text string, out *strings.Builder, stripped *bool) {
	// Locate each "<!--" at the start of a line (allowing leading
	// whitespace) and replace the span `<!--...?-->` with "".
	// Unclosed comments fall through unchanged.
	idx := 0
	for idx < len(text) {
		// Look for a `<!--` at column 0 of some line in text[idx:].
		remaining := text[idx:]
		pos := findBlockCommentStart(remaining)
		if pos < 0 {
			out.WriteString(remaining)
			return
		}
		// Write everything up to the comment.
		out.WriteString(remaining[:pos])
		// Find the matching `-->`.
		endRel := strings.Index(remaining[pos:], "-->")
		if endRel < 0 {
			// Unclosed: preserve verbatim.
			out.WriteString(remaining[pos:])
			return
		}
		*stripped = true
		// Advance idx past the closing `-->`.
		idx += pos + endRel + len("-->")
		// Per TS, any trailing content on the closing line is kept.
		// We don't special-case residue because the regex already
		// replaces the exact span.
	}
}

// findBlockCommentStart returns the offset of the first `<!--` in s
// that appears at the start of a line (preceded only by whitespace or
// a newline or start-of-string). Returns -1 when none found.
func findBlockCommentStart(s string) int {
	start := 0
	for start < len(s) {
		idx := strings.Index(s[start:], "<!--")
		if idx < 0 {
			return -1
		}
		abs := start + idx
		// Verify it is at column 0 of its line.
		lineStart := abs
		for lineStart > 0 && s[lineStart-1] != '\n' {
			lineStart--
		}
		prefix := s[lineStart:abs]
		if strings.TrimLeft(prefix, " \t") == "" {
			return abs
		}
		// Inline occurrence — skip past it and continue.
		start = abs + len("<!--")
	}
	return -1
}

// ---------------------------------------------------------------------------
// M3.I4 · ExtractIncludePaths
// ---------------------------------------------------------------------------

// includeTokenRe matches `@<path>` occurrences. The path is the
// longest run of non-space characters, treating `\ ` (backslash-space)
// as an escaped literal space. Mirrors the TS regex
// `(?:^|\s)@((?:[^\s\\]|\\ )+)`.
var includeTokenRe = regexp.MustCompile(`(?m)(?:^|[ \t])@((?:[^\s\\]|\\ )+)`)

// leadingSpecialPrefixRe matches tokens like `#foo`, `%bar`, `^baz`
// whose leading character is not a valid include path start.
var leadingSpecialPrefixRe = regexp.MustCompile(`^[#%^&*()]+`)

// startCharValidRe matches the set of characters allowed as the first
// rune of a raw (unprefixed) include path. Mirrors TS
// `^[a-zA-Z0-9._-]`.
var startCharValidRe = regexp.MustCompile(`^[a-zA-Z0-9._-]`)

// ExtractIncludePaths scans markdown content for `@path` directives
// and returns resolved absolute paths. basePath is the path of the
// file doing the including — its directory is used as the root for
// relative references.
//
// Accepted forms (mirroring paths.ts behaviour):
//
//   - `@./relative/path.md`
//   - `@~/home/path.md`
//   - `@/absolute/path.md`
//   - `@bare-name.md`  (treated as relative to dirname(basePath))
//
// Rejected:
//
//   - `@<illegal-start>` where illegal-start ∈ `#%^&*()`.
//   - Paths with an extension that is NOT in textFileExtensions.
//     Paths without an extension are always accepted.
//
// Fragments (`#heading`) are stripped before extension checking.
// Escaped spaces (`\ `) in the captured path are unescaped. Duplicates
// are collapsed via a set so the returned slice is order-stable but
// unique.
func ExtractIncludePaths(content, basePath string) []string {
	if !strings.Contains(content, "@") {
		return nil
	}
	baseDir := filepath.Dir(basePath)

	matches := includeTokenRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]struct{}, len(matches))
	var out []string

	for _, m := range matches {
		raw := m[1]
		if raw == "" {
			continue
		}
		// Drop fragment identifier.
		if i := strings.Index(raw, "#"); i >= 0 {
			raw = raw[:i]
		}
		if raw == "" {
			continue
		}
		// Unescape `\ ` → ` `.
		raw = strings.ReplaceAll(raw, `\ `, " ")
		if !isValidIncludePath(raw) {
			continue
		}
		// Filter by extension whitelist.
		ext := strings.ToLower(filepath.Ext(raw))
		if ext != "" && !IsTextFileExtension(ext) {
			continue
		}
		abs, ok := resolveIncludePath(raw, baseDir)
		if !ok {
			continue
		}
		if _, dup := seen[abs]; dup {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

// isValidIncludePath mirrors the TS `isValidPath` branch, extended
// with a platform-aware `filepath.IsAbs` check so Windows-style
// absolute paths (e.g. `C:/Users/foo/note.md`) are accepted even
// though they don't start with `/`.
func isValidIncludePath(path string) bool {
	switch {
	case strings.HasPrefix(path, "./"):
		return true
	case strings.HasPrefix(path, "~/"):
		return true
	case strings.HasPrefix(path, "/") && path != "/":
		return true
	case filepath.IsAbs(path):
		return true
	case strings.HasPrefix(path, "@"):
		return false
	case leadingSpecialPrefixRe.MatchString(path):
		return false
	case startCharValidRe.MatchString(path):
		return true
	}
	return false
}

// resolveIncludePath converts a raw include token into an absolute
// path. `~/foo` expands against os.UserHomeDir; `./foo` and bare
// names expand against baseDir; absolute paths (either `/...` POSIX
// or `C:/...` / `C:\...` Windows) pass through unchanged.
func resolveIncludePath(raw, baseDir string) (string, bool) {
	switch {
	case strings.HasPrefix(raw, "~/"):
		home, err := homeDirFn()
		if err != nil || home == "" {
			return "", false
		}
		return filepath.Clean(filepath.Join(home, raw[2:])), true
	case strings.HasPrefix(raw, "/"):
		return filepath.Clean(raw), true
	case filepath.IsAbs(raw):
		return filepath.Clean(raw), true
	case strings.HasPrefix(raw, "./"):
		return filepath.Clean(filepath.Join(baseDir, raw[2:])), true
	default:
		return filepath.Clean(filepath.Join(baseDir, raw)), true
	}
}

// homeDirFn is indirected via a package-level variable so tests can
// stub home-directory resolution.
var homeDirFn = osUserHomeDir

// ---------------------------------------------------------------------------
// M3.I5 · SplitPathInFrontmatter + brace expansion
// ---------------------------------------------------------------------------

// SplitPathInFrontmatter accepts either a comma-separated string OR
// a []interface{} / []string from YAML, expands brace patterns, and
// returns the flattened glob list. Whitespace around each comma-
// separated token is trimmed. Empty tokens are dropped.
//
// Examples (matched byte-identical to TS):
//
//	"a, b"                    → ["a", "b"]
//	"src/*.{ts,tsx}"           → ["src/*.ts", "src/*.tsx"]
//	"{a,b}/{c,d}"              → ["a/c", "a/d", "b/c", "b/d"]
//	[]string{"a", "src/*.{ts,tsx}"} → ["a", "src/*.ts", "src/*.tsx"]
//	nil                        → nil
func SplitPathInFrontmatter(input interface{}) []string {
	switch v := input.(type) {
	case nil:
		return nil
	case string:
		return splitPathString(v)
	case []string:
		var out []string
		for _, s := range v {
			out = append(out, splitPathString(s)...)
		}
		return out
	case []interface{}:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, splitPathString(s)...)
			}
		}
		return out
	}
	return nil
}

// splitPathString handles the single-string branch: brace-aware
// comma split, brace expansion, duplicate-safe output.
func splitPathString(s string) []string {
	parts := braceAwareCommaSplit(s)
	var out []string
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, expandBraces(trimmed)...)
		}
	}
	return out
}

// braceAwareCommaSplit splits on commas that are NOT inside `{...}`
// groups. Brace depth is tracked with a simple counter; nested braces
// are supported.
func braceAwareCommaSplit(s string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '{':
			depth++
			cur.WriteByte(c)
		case '}':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
		case ',':
			if depth == 0 {
				out = append(out, cur.String())
				cur.Reset()
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// braceGroupRe matches the first `{alternatives}` group in a pattern,
// capturing (prefix, alternatives, suffix).
var braceGroupRe = regexp.MustCompile(`^([^{]*)\{([^}]+)\}(.*)$`)

// expandBraces recursively expands a single pattern like
// `src/*.{ts,tsx}` into `["src/*.ts", "src/*.tsx"]`. Patterns with
// no braces pass through unchanged.
func expandBraces(pattern string) []string {
	m := braceGroupRe.FindStringSubmatch(pattern)
	if m == nil {
		return []string{pattern}
	}
	prefix, alternatives, suffix := m[1], m[2], m[3]
	parts := strings.Split(alternatives, ",")
	var out []string
	for _, part := range parts {
		combined := prefix + strings.TrimSpace(part) + suffix
		out = append(out, expandBraces(combined)...)
	}
	return out
}
