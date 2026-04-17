// Package powershell implements the Windows PowerShell tool. It mirrors the
// Bash tool's API and security model (precision-reduced deny list + read-only
// allowlist) but targets `powershell.exe -NoProfile -Command`.
package powershell

import (
	"regexp"
	"strings"
)

// Class enumerates the security classification.
type Class int

const (
	ClassNormal Class = iota
	ClassReadOnly
	ClassDeny
)

// Classification is the result of Classify.
type Classification struct {
	Class  Class
	Reason string
}

var denyPatterns = []struct {
	re     *regexp.Regexp
	reason string
}{
	{regexp.MustCompile(`(?i)\bRemove-Item\b[^|;&]*\s-Recurse\b[^|;&]*\s-Force\b[^|;&]*\s(C:\\|\$HOME|~)`), "Remove-Item -Recurse -Force on root/home"},
	{regexp.MustCompile(`(?i)\bRemove-Item\b[^|;&]*\s-Force\b[^|;&]*\s-Recurse\b[^|;&]*\s(C:\\|\$HOME|~)`), "Remove-Item -Force -Recurse on root/home"},
	{regexp.MustCompile(`(?i)\bInvoke-Expression\b[^|;&]*\(New-Object\s+Net\.WebClient\)`), "IEX remote download"},
	{regexp.MustCompile(`(?i)\bIEX\b[^|;&]*\(\s*New-Object\s+Net\.WebClient`), "IEX remote download"},
	{regexp.MustCompile(`(?i)\bInvoke-WebRequest\b[^|]*\|\s*(IEX|Invoke-Expression)\b`), "IWR | IEX remote exec"},
	{regexp.MustCompile(`(?i)\bSet-ExecutionPolicy\b[^|;&]*\bUnrestricted\b`), "ExecutionPolicy Unrestricted"},
	{regexp.MustCompile(`(?i)\bFormat-Volume\b`), "Format-Volume"},
	{regexp.MustCompile(`(?i)\bStop-Computer\b|\bRestart-Computer\b`), "power state change"},
	{regexp.MustCompile(`(?i)\bClear-Disk\b|\bRemove-Partition\b`), "disk destructive"},
	{regexp.MustCompile(`(?i)\bcmd(\.exe)?\b[^|;&]*\s/c\s+rd\s+/s\s+/q\s+(c:\\|%.+%)`), "cmd rd /s /q on root"},
}

var readOnlyPrefixes = []string{
	"get-childitem", "gci", "ls", "dir",
	"get-content", "gc", "cat", "type",
	"get-location", "pwd", "gl",
	"get-item", "gi", "get-itemproperty",
	"select-string", "sls",
	"measure-object", "measure",
	"sort-object", "sort",
	"where-object", "where",
	"select-object", "select",
	"foreach-object", "foreach", "%",
	"format-list", "format-table", "fl", "ft",
	"out-string", "out-host",
	"get-process", "ps",
	"get-service",
	"get-command", "gcm",
	"get-help", "help", "man",
	"get-date", "date",
	"get-alias", "get-variable",
	"test-path",
	"resolve-path",
	"echo", "write-output", "write-host",
	"git status", "git diff", "git log", "git show", "git branch", "git rev-parse", "git remote", "git ls-files",
	"go version", "go env", "go list", "go doc", "go vet",
	"node -v", "node --version", "npm -v", "npm --version", "npm list", "npm ls",
	"python -v", "python --version", "python3 -v", "python3 --version",
	"$psversiontable",
}

// Classify categorises a PowerShell command line.
func Classify(cmdline string) Classification {
	s := strings.TrimSpace(cmdline)
	if s == "" {
		return Classification{Class: ClassNormal}
	}
	for _, d := range denyPatterns {
		if d.re.MatchString(s) {
			return Classification{Class: ClassDeny, Reason: d.reason}
		}
	}
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
			out = append(out, cur.String())
			cur.Reset()
		case ';':
			out = append(out, cur.String())
			cur.Reset()
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
	lower := strings.ToLower(strings.TrimLeft(segment, " \t"))
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
