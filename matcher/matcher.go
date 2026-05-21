// Package matcher validates (path, argv) against the allowlist.
//
// Each rule's CommandPatterns is a per-element regex list — index 0
// matches the binary path, indices 1..N-1 match argv 1-for-1. The
// per-element shape means ["foo bar"] (one element with a space) and
// ["foo","bar"] (two elements) cannot be confused: the matcher counts
// them separately, so a regex written for two args can't be fooled by
// a single joined element.
package matcher

import (
	"strings"

	"github.com/binwiederhier/rrsh/config"
)

// Matcher checks (path, argv) pairs against a fixed rule set. Safe to
// reuse across calls; rules are not mutated.
type Matcher struct {
	rules []config.CommandRule
}

func New(rules []config.CommandRule) *Matcher {
	return &Matcher{rules: rules}
}

// Match returns the first rule whose command[0] matches path AND whose
// argv shape matches. Multiple rules with the same command[0] coexist
// as alternative argv shapes (e.g. `ps aux` and `ps -ef`); the matcher
// tries each in declaration order.
func (m *Matcher) Match(path string, argv []string) (*config.CommandRule, bool) {
	// Defense-in-depth: refuse relative paths even if an operator wrote
	// an over-permissive command[0] regex, since exec would PATH-resolve.
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	for i := range m.rules {
		rule := &m.rules[i]
		if matches(path, argv, rule) {
			return rule, true
		}
	}
	return nil, false
}

// matches reports whether (path, argv) satisfies one rule.
func matches(path string, argv []string, rule *config.CommandRule) bool {
	if len(rule.CommandPatterns) == 0 {
		return false
	}
	if !rule.CommandPatterns[0].MatchString(path) {
		return false
	}
	if len(argv) != len(rule.CommandPatterns)-1 {
		return false
	}
	for i, a := range argv {
		if !rule.CommandPatterns[i+1].MatchString(a) {
			return false
		}
	}
	return true
}
