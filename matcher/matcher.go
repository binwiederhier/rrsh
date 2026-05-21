// Package matcher validates an (absolute path, argv) pair against the
// configured allowlist. Each rule declares a list of regexes — element 0
// matches the binary path; elements 1..N-1 match argv elements. A call
// passes when its path satisfies command[0] and its argv has exactly
// len(command)-1 elements, each matching its corresponding regex.
//
// There is no shell-string parsing here: argv arrives as a []string from
// a JSON-RPC client, so quoting and metacharacters in argument values
// are just bytes inside individual array elements, not parser hazards.
// The per-element design means ["foo bar"] (one element with a space)
// and ["foo","bar"] (two elements) are structurally distinct — the
// matcher counts elements separately, so an operator's regex designed
// for two args can't be silently fooled by a single element joined into
// the same string.
package matcher

import (
	"strings"

	"github.com/binwiederhier/rrsh/config"
)

// Matcher checks (path, argv) pairs against a fixed set of CommandRules.
// It is safe to reuse a Matcher across many Match calls; the rules are
// not mutated.
type Matcher struct {
	rules []config.CommandRule
}

// New returns a Matcher that resolves calls against the given rules in
// declaration order.
func New(rules []config.CommandRule) *Matcher {
	return &Matcher{rules: rules}
}

// Match returns the matching rule and true if (path, argv) is allowed by
// any rule. Rules are scanned in declaration order; the first rule whose
// command[0] matches path AND whose argv-shape matches is returned.
//
// Multiple rules with the same command[0] are allowed and useful — e.g.
// one rule for `ps aux` and another for `ps -ef` coexist as separate
// entries, and the matcher tries each in turn until one accepts. If no
// rule matches, returns nil, false.
func (m *Matcher) Match(path string, argv []string) (*config.CommandRule, bool) {
	// Defense-in-depth: even if an operator writes an over-permissive
	// command[0] regex, refuse to dispatch a relative path. The exec
	// layer would happily PATH-resolve "rm" → /usr/bin/rm and run it.
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	for i := range m.rules {
		rule := &m.rules[i]
		if argvMatchesRule(path, argv, rule) {
			return rule, true
		}
	}
	return nil, false
}

// argvMatchesRule reports whether (path, argv) satisfies a single rule.
// Lengths must match exactly: rule.CommandPatterns has 1 entry for the
// path plus one per expected argv element.
func argvMatchesRule(path string, argv []string, rule *config.CommandRule) bool {
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
