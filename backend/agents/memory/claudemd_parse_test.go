package memory

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// M3.T1 · Frontmatter parsing
// ---------------------------------------------------------------------------

func TestParseFrontmatter_NoFence(t *testing.T) {
	in := "# Hello\n\njust body text\n"
	got := ParseFrontmatter(in)
	if got.Content != in {
		t.Errorf("expected body passthrough; got %q", got.Content)
	}
	if got.Frontmatter.Paths != nil || got.Frontmatter.Type != "" {
		t.Errorf("expected empty frontmatter; got %+v", got.Frontmatter)
	}
}

func TestParseFrontmatter_Simple(t *testing.T) {
	in := "---\ntype: user\n---\nbody here\n"
	got := ParseFrontmatter(in)
	if got.Frontmatter.Type != "user" {
		t.Errorf("type: %q; want %q", got.Frontmatter.Type, "user")
	}
	if got.Content != "body here\n" {
		t.Errorf("content: %q; want %q", got.Content, "body here\n")
	}
}

func TestParseFrontmatter_PathsListAndString(t *testing.T) {
	// As YAML list
	list := "---\npaths:\n  - src/a.md\n  - src/b.md\n---\n"
	g1 := ParseFrontmatter(list)
	globs := SplitPathInFrontmatter(g1.Frontmatter.Paths)
	if !reflect.DeepEqual(globs, []string{"src/a.md", "src/b.md"}) {
		t.Errorf("list: got %v", globs)
	}

	// As comma-separated string — must trigger quoteProblematicValues retry
	// because of `*` and `{}`.
	str := "---\npaths: src/*.{ts,tsx}, docs/*.md\n---\n"
	g2 := ParseFrontmatter(str)
	globs2 := SplitPathInFrontmatter(g2.Frontmatter.Paths)
	want := []string{"src/*.ts", "src/*.tsx", "docs/*.md"}
	if !reflect.DeepEqual(globs2, want) {
		t.Errorf("string: got %v; want %v", globs2, want)
	}
}

func TestParseFrontmatter_MalformedFallback(t *testing.T) {
	// Unclosed fence — no match, return passthrough.
	in := "---\nnot closed"
	got := ParseFrontmatter(in)
	if got.Content != in {
		t.Errorf("malformed passthrough broken: %q", got.Content)
	}
}

// ---------------------------------------------------------------------------
// M3.T1 · quoteProblematicValues
// ---------------------------------------------------------------------------

func TestQuoteProblematicValues(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{`paths: src/*.{ts,tsx}`, `paths: "src/*.{ts,tsx}"`},
		{`paths: "already quoted"`, `paths: "already quoted"`},
		{`plain: value`, `plain: value`},
		{`key: value: with colon`, `key: "value: with colon"`},
		{`key: simple`, `key: simple`},
	}
	for _, tc := range cases {
		got := quoteProblematicValues(tc.in)
		if got != tc.out {
			t.Errorf("input %q:\n got  %q\n want %q", tc.in, got, tc.out)
		}
	}
}

// ---------------------------------------------------------------------------
// M3.T1 · StripHtmlComments
// ---------------------------------------------------------------------------

func TestStripHtmlComments(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		want       string
		wantStrip  bool
	}{
		{
			name:      "no-comments",
			in:        "hello\nworld\n",
			want:      "hello\nworld\n",
			wantStrip: false,
		},
		{
			name:      "single-block-comment",
			in:        "line1\n<!-- hidden -->\nline3\n",
			want:      "line1\n\nline3\n",
			wantStrip: true,
		},
		{
			name:      "multi-line-comment",
			in:        "before\n<!-- line1\nline2\n-->\nafter\n",
			want:      "before\n\nafter\n",
			wantStrip: true,
		},
		{
			name:      "unclosed-comment-preserved",
			in:        "before\n<!-- oops\nmore text\n",
			want:      "before\n<!-- oops\nmore text\n",
			wantStrip: false,
		},
		{
			name:      "fenced-code-block-preserves",
			in:        "```\n<!-- this stays -->\n```\nafter\n",
			want:      "```\n<!-- this stays -->\n```\nafter\n",
			wantStrip: false,
		},
		{
			name:      "inline-comment-in-paragraph-not-stripped",
			in:        "text <!-- ignored by spec --> more\n",
			want:      "text <!-- ignored by spec --> more\n",
			wantStrip: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, s := StripHtmlComments(tc.in)
			if got != tc.want {
				t.Errorf("content mismatch\n got  %q\n want %q", got, tc.want)
			}
			if s != tc.wantStrip {
				t.Errorf("stripped flag: got %v want %v", s, tc.wantStrip)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M3.T1 · ExtractIncludePaths
// ---------------------------------------------------------------------------

func TestExtractIncludePaths(t *testing.T) {
	home := t.TempDir()
	homeDirFn = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDirFn = osUserHomeDir })

	base := filepath.Join(t.TempDir(), "project", "CLAUDE.md")

	cases := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "relative-dot",
			in:   "See @./docs/guide.md for details.",
			want: []string{filepath.Join(filepath.Dir(base), "docs", "guide.md")},
		},
		{
			name: "bare-name",
			in:   "Load @guide.md now",
			want: []string{filepath.Join(filepath.Dir(base), "guide.md")},
		},
		{
			name: "home-expansion",
			in:   "See @~/private/notes.md",
			want: []string{filepath.Join(home, "private", "notes.md")},
		},
		{
			name: "absolute-path",
			in:   "See @/etc/claude/rule.md",
			want: []string{filepath.Clean("/etc/claude/rule.md")},
		},
		{
			name: "strip-fragment",
			in:   "See @./guide.md#section",
			want: []string{filepath.Join(filepath.Dir(base), "guide.md")},
		},
		{
			name: "reject-bad-ext",
			in:   "See @./image.png",
			want: nil,
		},
		{
			name: "reject-leading-specials",
			in:   "See @#foo or @%bar",
			want: nil,
		},
		{
			name: "no-extension-accepted",
			in:   "See @./guide",
			want: []string{filepath.Join(filepath.Dir(base), "guide")},
		},
		{
			name: "multiple-dedup",
			in:   "@./a.md and @./a.md twice",
			want: []string{filepath.Join(filepath.Dir(base), "a.md")},
		},
		{
			name: "escaped-space",
			in:   `@./my\ file.md`,
			want: []string{filepath.Join(filepath.Dir(base), "my file.md")},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractIncludePaths(tc.in, base)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			// Order-insensitive comparison for robustness against
			// map-iteration noise in the de-duplication set.
			a := append([]string(nil), got...)
			b := append([]string(nil), tc.want...)
			sort.Strings(a)
			sort.Strings(b)
			if !reflect.DeepEqual(a, b) {
				t.Errorf("got %v\nwant %v", a, b)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// M3.T1 · SplitPathInFrontmatter
// ---------------------------------------------------------------------------

func TestSplitPathInFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		in   interface{}
		want []string
	}{
		{"nil", nil, nil},
		{"simple-string", "a, b", []string{"a", "b"}},
		{"brace-expand", "src/*.{ts,tsx}", []string{"src/*.ts", "src/*.tsx"}},
		{"multiple-brace", "{a,b}/{c,d}", []string{"a/c", "a/d", "b/c", "b/d"}},
		{"yaml-list", []interface{}{"a", "b"}, []string{"a", "b"}},
		{"string-slice", []string{"a", "src/*.{js,ts}"}, []string{"a", "src/*.js", "src/*.ts"}},
		{"empty-string", "", nil},
		{"trim-whitespace", "  a  ,  b  ", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitPathInFrontmatter(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v\nwant %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Sanity check for isValidIncludePath edge cases.
// ---------------------------------------------------------------------------

func TestIsValidIncludePath(t *testing.T) {
	valid := []string{"./a", "~/b", "/c", "a", "A", "1file", "./nested/file.md"}
	for _, v := range valid {
		if !isValidIncludePath(v) {
			t.Errorf("should accept %q", v)
		}
	}
	invalid := []string{"@x", "#y", "%z", "&q", "/"}
	for _, v := range invalid {
		if isValidIncludePath(v) {
			t.Errorf("should reject %q", v)
		}
	}
}

// Diagnostic: the multi-line comment test emits a partially stripped
// residue that still contains `\n`. Ensure our builder does not swallow
// surrounding newlines so downstream markdown stays readable.
func TestStripHtmlComments_LeavesSurroundingNewlines(t *testing.T) {
	in := "# title\n\n<!-- block -->\n\nbody\n"
	got, _ := StripHtmlComments(in)
	if !strings.Contains(got, "# title") || !strings.Contains(got, "body") {
		t.Errorf("surrounding text dropped: %q", got)
	}
}
