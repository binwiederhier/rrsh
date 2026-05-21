// Package matcher validates an (absolute path, argv) pair against the
// configured allowlist. Each rule declares a list of per-argv-element
// regexes; a call matches when argv has the same length as the rule's
// pattern list and every element passes its corresponding regex.
//
// There is no shell-string parsing here: argv arrives as a []string from
// a JSON-RPC client, so quoting and metacharacters in argument values
// are just bytes inside individual array elements, not parser hazards.
// Crucially, the per-element design means ["foo bar"] (one element with
// a space) and ["foo","bar"] (two elements) are structurally distinct —
// the matcher counts elements separately, so an operator's regex
// designed for two args can't be silently fooled by a single element
// joined into the same string.
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
// Path equals path AND whose argv shape matches is returned.
//
// Multiple rules with the same Path are allowed and useful — e.g. one
// rule for `ps aux` (one argv) and another for `ps -eo <fmt>` (two
// argv) coexist as separate entries, and the matcher tries each in turn
// until one accepts. If no rule matches, returns nil, false.
func (m *Matcher) Match(path string, argv []string) (*config.CommandRule, bool) {
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	for i := range m.rules {
		if m.rules[i].Path != path {
			continue
		}
		if argvMatchesRule(argv, &m.rules[i]) {
			return &m.rules[i], true
		}
	}
	return nil, false
}

// argvMatchesRule reports whether argv satisfies a single rule's args
// constraint. A nil ArgsPatterns means any argv; otherwise lengths must
// match and every pattern[i] must accept argv[i].
func argvMatchesRule(argv []string, rule *config.CommandRule) bool {
	if rule.ArgsPatterns == nil {
		return true
	}
	if len(argv) != len(rule.ArgsPatterns) {
		return false
	}
	for i, pat := range rule.ArgsPatterns {
		if !pat.MatchString(argv[i]) {
			return false
		}
	}
	return true
}
