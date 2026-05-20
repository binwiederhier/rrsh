// Package matcher validates an (absolute path, argv) pair against the
// configured allowlist. The argv is matched as a single whitespace-joined
// string against the rule's compiled regex — the regex is the contract
// between the config author and the caller.
//
// There is no shell-string parsing here: argv arrives as a []string from a
// JSON-RPC client, so quoting and metacharacters in argument values are
// just bytes inside individual array elements, not parser hazards.
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
// any rule. Rules are scanned in order; the first rule whose Path equals
// path decides the outcome — if its ArgsPattern does not match the
// space-joined argv, the call is denied (later rules with the same path
// are not tried). Returns nil, false otherwise.
func (m *Matcher) Match(path string, argv []string) (*config.CommandRule, bool) {
	if !strings.HasPrefix(path, "/") {
		return nil, false
	}
	args := strings.Join(argv, " ")
	for i := range m.rules {
		if m.rules[i].Path != path {
			continue
		} else if m.rules[i].ArgsPattern == nil {
			return &m.rules[i], true
		} else if m.rules[i].ArgsPattern.MatchString(args) {
			return &m.rules[i], true
		}
		return nil, false
	}
	return nil, false
}
