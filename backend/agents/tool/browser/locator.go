package browser

import (
	"fmt"
	"regexp"
	"strings"
)

// LocatorStrategy identifies the type of element lookup to use with rod.
type LocatorStrategy int

const (
	StrategyCSS   LocatorStrategy = iota
	StrategyXPath
)

// ResolvedLocator is the parsed output of Resolve().
type ResolvedLocator struct {
	Strategy LocatorStrategy
	Value    string
}

// Resolve parses a DrissionPage-style locator string into a strategy+value pair.
//
// Supported formats (16 patterns, evaluated in priority order):
//
//	#id / .class / tag[attr]         → CSS
//	css=sel / c=sel                  → CSS
//	xpath=expr / x=expr / //tag      → XPath
//	text=登录                        → XPath exact text
//	text:搜索                        → XPath contains text
//	text^开头                        → XPath starts-with text
//	text$结尾                        → XPath ends-with (substring)
//	tag:div@class=foo                → XPath //div[@class='foo']
//	@@a=v1@@b=v2                     → XPath AND multi-attr
//	@|a=v1@@b=v2                     → XPath OR  multi-attr
//	@!a=v1                           → XPath NOT attr
//	@attr=val                        → XPath //*[@attr='val']
//	(default) plain text             → XPath fuzzy contains
func Resolve(locator string) ResolvedLocator {
	loc := strings.TrimSpace(locator)
	if loc == "" {
		return ResolvedLocator{StrategyCSS, "*"}
	}

	// 1. CSS shortcuts: starts with # . [ or contains > + ~
	if loc[0] == '#' || loc[0] == '.' || loc[0] == '[' {
		return ResolvedLocator{StrategyCSS, loc}
	}

	// 2. Explicit CSS prefix
	for _, prefix := range []string{"css=", "c="} {
		if strings.HasPrefix(loc, prefix) {
			return ResolvedLocator{StrategyCSS, loc[len(prefix):]}
		}
	}

	// 3. Explicit XPath prefix
	for _, prefix := range []string{"xpath=", "x="} {
		if strings.HasPrefix(loc, prefix) {
			return ResolvedLocator{StrategyXPath, loc[len(prefix):]}
		}
	}

	// 4. XPath literal (starts with // or /)
	if strings.HasPrefix(loc, "//") || (strings.HasPrefix(loc, "/") && len(loc) > 1) {
		return ResolvedLocator{StrategyXPath, loc}
	}

	// 5. text= exact match
	if strings.HasPrefix(loc, "text=") {
		text := loc[5:]
		return ResolvedLocator{StrategyXPath, fmt.Sprintf(`//*[text()=%s]`, xpathQuote(text))}
	}

	// 6. text: contains
	if strings.HasPrefix(loc, "text:") {
		text := loc[5:]
		return ResolvedLocator{StrategyXPath, fmt.Sprintf(`//*/text()[contains(.,%s)]/..`, xpathQuote(text))}
	}

	// 7. text^ starts-with
	if strings.HasPrefix(loc, "text^") {
		text := loc[5:]
		return ResolvedLocator{StrategyXPath, fmt.Sprintf(`//*[starts-with(text(),%s)]`, xpathQuote(text))}
	}

	// 8. text$ ends-with (XPath 1.0 substring workaround)
	if strings.HasPrefix(loc, "text$") {
		text := loc[5:]
		return ResolvedLocator{StrategyXPath, fmt.Sprintf(
			`//*[substring(text(),string-length(text())-string-length(%s)+1)=%s]`,
			xpathQuote(text), xpathQuote(text))}
	}

	// 9. tag:tagname or tag:tagname@attr=val
	if strings.HasPrefix(loc, "tag:") {
		return resolveTagLocator(loc[4:])
	}

	// 10. @@multi-attr AND
	if strings.HasPrefix(loc, "@@") {
		return resolveMultiAttr(loc[2:], "and")
	}

	// 11. @| multi-attr OR
	if strings.HasPrefix(loc, "@|") {
		return resolveMultiAttr(loc[2:], "or")
	}

	// 12. @! attr negation
	if strings.HasPrefix(loc, "@!") {
		cond := parseSingleAttr(loc[2:])
		return ResolvedLocator{StrategyXPath, fmt.Sprintf(`//*[not(%s)]`, cond)}
	}

	// 13. @attr=val single attribute
	if strings.HasPrefix(loc, "@") && strings.Contains(loc, "=") {
		cond := parseSingleAttr(loc[1:])
		return ResolvedLocator{StrategyXPath, fmt.Sprintf(`//*[%s]`, cond)}
	}

	// 14. If it looks like a CSS selector (contains combinators)
	if cssSelectorRe.MatchString(loc) {
		return ResolvedLocator{StrategyCSS, loc}
	}

	// 15. Default: fuzzy text contains
	return ResolvedLocator{StrategyXPath, fmt.Sprintf(`//*[contains(text(),%s)]`, xpathQuote(loc))}
}

// cssSelectorRe matches patterns that are almost certainly CSS selectors.
var cssSelectorRe = regexp.MustCompile(`[>+~\[\]:]`)

// resolveTagLocator parses "div@class=foo" → //div[@class='foo']
func resolveTagLocator(s string) ResolvedLocator {
	// Split tag and attrs
	parts := strings.SplitN(s, "@", 2)
	tag := strings.TrimSpace(parts[0])
	if tag == "" {
		tag = "*"
	}
	if len(parts) == 1 {
		return ResolvedLocator{StrategyXPath, fmt.Sprintf("//%s", tag)}
	}
	// May have multiple attrs separated by @
	attrStr := parts[1]
	conds := parseAttrConditions(attrStr, "and")
	return ResolvedLocator{StrategyXPath, fmt.Sprintf("//%s[%s]", tag, conds)}
}

// resolveMultiAttr parses "a=v1@@b=v2" with given operator ("and"/"or").
func resolveMultiAttr(s string, op string) ResolvedLocator {
	conds := parseAttrConditions(s, op)
	return ResolvedLocator{StrategyXPath, fmt.Sprintf("//*[%s]", conds)}
}

// parseAttrConditions splits "a=v1@@b=v2" into XPath conditions joined by op.
func parseAttrConditions(s string, op string) string {
	pairs := strings.Split(s, "@@")
	var parts []string
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts = append(parts, parseSingleAttr(pair))
	}
	if len(parts) == 0 {
		return "true()"
	}
	return strings.Join(parts, fmt.Sprintf(" %s ", op))
}

// parseSingleAttr converts "attr=val" → "@attr='val'" (XPath condition).
func parseSingleAttr(s string) string {
	idx := strings.Index(s, "=")
	if idx < 0 {
		// Attribute presence check
		return fmt.Sprintf("@%s", s)
	}
	attr := s[:idx]
	val := s[idx+1:]
	return fmt.Sprintf("@%s=%s", attr, xpathQuote(val))
}

// xpathQuote safely quotes a string for XPath, handling embedded quotes
// by using concat() when both single and double quotes are present.
func xpathQuote(s string) string {
	hasSingle := strings.Contains(s, "'")
	hasDouble := strings.Contains(s, `"`)

	if !hasSingle {
		return fmt.Sprintf("'%s'", s)
	}
	if !hasDouble {
		return fmt.Sprintf(`"%s"`, s)
	}

	// Both present — use concat()
	var parts []string
	for _, ch := range s {
		c := string(ch)
		if c == "'" {
			parts = append(parts, `"'"`)
		} else {
			parts = append(parts, fmt.Sprintf("'%s'", c))
		}
	}
	return fmt.Sprintf("concat(%s)", strings.Join(parts, ","))
}
