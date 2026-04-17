// Package bash implements the Unix Bash tool, a shell-executor wrapper
// around agents/tool/shell. Ships a *precision-reduced* security model: a
// deny list of known-dangerous patterns plus a conservative read-only
// allowlist. Anything not in either set routes to "ask" so the caller can
// surface a permission prompt.
package bash

import (
	"regexp"
	"strings"
)

// Class categorises a command line for permission purposes.
type Class int

const (
	// ClassNormal means the command is neither known-safe nor known-dangerous.
	ClassNormal Class = iota
	// ClassReadOnly means the command matches the read-only allowlist.
	ClassReadOnly
	// ClassDeny means the command matches a high-risk deny pattern.
	ClassDeny
)

// Classification is the result of Classify.
type Classification struct {
	Class  Class
	Reason string
}

// denyPatterns are regular expressions that match known-dangerous commands.
// Keep the patterns tight to avoid false positives on legitimate usage.
var denyPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`(?i)\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|-[a-zA-Z]*f[a-zA-Z]*r)\b[^|;&]*\s(/|~|\$HOME)(\s|$)`), "rm -rf on root/home path"},
	{regexp.MustCompile(`(?i):\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:`), "fork bomb"},
	{regexp.MustCompile(`(?i)\b(curl|wget)\b[^|]*\|\s*(sh|bash|zsh|python|perl|ruby)\b`), "curl|sh remote-exec"},
	{regexp.MustCompile(`(?i)^\s*sudo\b`), "sudo escalation"},
	{regexp.MustCompile(`(?i)\bmkfs(\.\w+)?\b`), "filesystem format"},
	{regexp.MustCompile(`(?i)\bdd\b[^|;&]*\bof=/dev/(sd|nvme|hd|vd)\w`), "dd to block device"},
	{regexp.MustCompile(`>\s*/dev/(sd|nvme|hd|vd)\w`), "redirect to block device"},
	{regexp.MustCompile(`(?i)\bchmod\b\s+-R?\s*777\s+/(?:\s|$)`), "chmod 777 root"},
	{regexp.MustCompile(`(?i)\bshutdown\b|\breboot\b|\bhalt\b|\bpoweroff\b`), "system power state"},
}

// readOnlyPrefixes is a set of command roots that only read state.
// Anything starting with one of these (after trimming and removing
// redirection/pipelines) is ClassReadOnly.
var readOnlyPrefixes = []string{
	"ls", "pwd", "cat", "head", "tail", "wc", "stat", "file", "which", "type", "whoami", "id", "date", "uname",
	"find", "grep", "egrep", "fgrep", "rg", "fd", "awk", "sed -n", "cut", "sort", "uniq", "diff", "cmp", "tree",
	"echo", "printf",
	"git status", "git diff", "git log", "git show", "git branch", "git rev-parse", "git remote", "git ls-files",
	"go version", "go env", "go list", "go doc", "go vet",
	"node -v", "node --version", "npm -v", "npm --version", "npm list", "npm ls",
	"python -V", "python --version", "python3 -V", "python3 --version", "pip list", "pip show",
	"env", "printenv",
}

// Classify categorises raw shell command text.
func Classify(cmdline string) Classification {
	s := strings.TrimSpace(cmdline)
	if s == "" {
		return Classification{Class: ClassNormal}
	}
	// Deny checks scan the full pipeline.
	for _, d := range denyPatterns {
		if d.re.MatchString(s) {
			return Classification{Class: ClassDeny, Reason: d.reason}
		}
	}
	// Read-only requires every pipeline segment to start with a read-only prefix.
	segments := splitPipelines(s)
	if len(segments) == 0 {
		return Classification{Class: ClassNormal}
	}
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			return Classification{Class: ClassNormal}
		}
		if !hasReadOnlyPrefix(seg) {
			return Classification{Class: ClassNormal}
		}
	}
	return Classification{Class: ClassReadOnly, Reason: "read-only allowlist"}
}

// splitPipelines splits on pipe/semicolon/&& while honoring simple quoting.
func splitPipelines(s string) []string {
	var out []string
	var cur strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '|':
			// "||" is logical OR, still a boundary.
			out = append(out, cur.String())
			cur.Reset()
			if i+1 < len(s) && s[i+1] == '|' {
				i++
			}
		case ';':
			out = append(out, cur.String())
			cur.Reset()
		case '&':
			if i+1 < len(s) && s[i+1] == '&' {
				out = append(out, cur.String())
				cur.Reset()
				i++
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

func hasReadOnlyPrefix(segment string) bool {
	seg := strings.TrimLeft(segment, " \t")
	// Strip leading env assignments (FOO=bar cmd ...).
	for {
		if i := strings.IndexByte(seg, '='); i > 0 && !strings.ContainsAny(seg[:i], " \t") {
			rest := strings.TrimLeft(seg[i+1:], " \t")
			// Skip token value (handle quoted).
			j := 0
			for j < len(rest) && rest[j] != ' ' && rest[j] != '\t' {
				j++
			}
			seg = strings.TrimLeft(rest[j:], " \t")
			continue
		}
		break
	}
	lower := strings.ToLower(seg)
	for _, p := range readOnlyPrefixes {
		if strings.HasPrefix(lower, p) {
			n := len(p)
			if n == len(lower) || lower[n] == ' ' || lower[n] == '\t' {
				return true
			}
		}
	}
	return false
}
