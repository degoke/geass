package platform

import (
	"regexp"
	"strings"
)

// WatchPatternsTrigger says whether a push that changed these files (paths from the repository root) should
// rebuild a service that watches the given patterns. The patterns are gitignore-style, one per entry:
//
//   - a pattern with a slash at its start or in its middle is matched from the repository root, and one
//     without a slash is matched at any depth, so "*.go" means every Go file and "/src" only the top one;
//   - "*" does not cross a slash, "**" does, and "?" is one character that is not a slash;
//   - a pattern that names a directory covers everything in it, and a trailing slash says it is one;
//   - a pattern that starts with "!" takes files back out, and the last pattern that matches a file decides it.
//
// A file triggers a build when the last pattern that matches it is not a "!" one. A file that no pattern
// matches does not, unless every pattern is a "!" one, which reads as "everything except": then it does.
// No patterns, or no changed files to look at, means every push triggers.
func WatchPatternsTrigger(patterns, changed []string) bool {
	rules := parseWatchPatterns(patterns)
	if len(rules) == 0 || len(changed) == 0 {
		return true
	}
	for _, file := range changed {
		if watchRulesInclude(rules, file) {
			return true
		}
	}
	return false
}

type watchRule struct {
	pattern *regexp.Regexp
	negated bool
}

func parseWatchPatterns(patterns []string) []watchRule {
	var rules []watchRule
	for _, raw := range patterns {
		pattern := strings.TrimSpace(raw)
		if pattern == "" || strings.HasPrefix(pattern, "#") {
			continue
		}
		negated := strings.HasPrefix(pattern, "!")
		pattern = strings.TrimPrefix(pattern, "!")
		if pattern = strings.TrimSpace(pattern); pattern == "" {
			continue
		}
		rules = append(rules, watchRule{pattern: compileWatchPattern(pattern), negated: negated})
	}
	return rules
}

func watchRulesInclude(rules []watchRule, file string) bool {
	file = strings.Trim(strings.ReplaceAll(file, "\\", "/"), "/")
	included := true
	for _, rule := range rules {
		if !rule.negated {
			included = false
			break
		}
	}
	for _, rule := range rules {
		if watchRuleMatches(rule.pattern, file) {
			included = !rule.negated
		}
	}
	return included
}

// watchRuleMatches tests the file and each directory above it, so a pattern that names a directory covers
// what is in it.
func watchRuleMatches(pattern *regexp.Regexp, file string) bool {
	for candidate := file; candidate != ""; {
		if pattern.MatchString(candidate) {
			return true
		}
		slash := strings.LastIndex(candidate, "/")
		if slash < 0 {
			break
		}
		candidate = candidate[:slash]
	}
	return false
}

func compileWatchPattern(pattern string) *regexp.Regexp {
	pattern = strings.TrimRight(pattern, "/")
	anchored := strings.Contains(strings.TrimPrefix(pattern, "/"), "/") || strings.HasPrefix(pattern, "/")
	pattern = strings.TrimPrefix(pattern, "/")
	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	// By rune: a byte of a multi-byte character is not a character, and quoting it as one corrupts the pattern.
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch c := runes[i]; c {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				i++
				if i+1 < len(runes) && runes[i+1] == '/' {
					// "**/" is any number of directories, including none.
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
				continue
			}
			b.WriteString("[^/]*")
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
