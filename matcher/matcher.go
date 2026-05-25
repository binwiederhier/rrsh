// Package matcher resolves a command to its allowlist rule and
// authorizes a target user against the rule's as: list.
package matcher

import (
	"slices"
	"strings"

	"github.com/binwiederhier/rrsh/config"
)

// Matcher resolves a command to its allowlist rule and authorizes a
// target user against the rule's `as:` list. Safe to reuse across
// calls; rules are not mutated.
type Matcher struct {
	rules []config.CommandRule
	user  string
}

// New constructs a Matcher bound to user. The user is the default
// "as:" target (what `MatchAsUser("")` resolves to) and the implicit
// authorized identity when a rule's `as:` list is empty.
func New(rules []config.CommandRule, user string) *Matcher {
	return &Matcher{rules: rules, user: user}
}

// Match returns the first rule whose command pattern matches AND whose
// `as:` list authorizes the matcher's user. Shorthand for
// MatchAsUser(command, "").
func (m *Matcher) Match(command []string) (*config.CommandRule, bool) {
	return m.MatchAsUser(command, "")
}

// MatchAsUser returns the first rule whose command pattern matches AND
// whose `as:` list authorizes asUser. An empty asUser defaults to the
// matcher's user. Authorization rule:
//   - rule.As empty -> only the matcher's user is allowed
//   - rule.As non-empty -> asUser must be in the list (literal match)
//
// command[0] is the binary path; command[1:] is argv.
func (m *Matcher) MatchAsUser(command []string, asUser string) (*config.CommandRule, bool) {
	if asUser == "" {
		asUser = m.user
	}
	// Defense-in-depth: refuse relative paths even if an operator wrote
	// an over-permissive command[0] regex, since exec would PATH-resolve.
	if len(command) == 0 || !strings.HasPrefix(command[0], "/") {
		return nil, false
	}
	for i := range m.rules {
		rule := &m.rules[i]
		if !patternMatches(command, rule) || !m.authorized(rule, asUser) {
			continue
		}
		return rule, true
	}
	return nil, false
}

// authorized: empty rule.As means "matcher's user only"; non-empty
// means asUser must appear in the list verbatim.
func (m *Matcher) authorized(rule *config.CommandRule, asUser string) bool {
	if len(rule.As) == 0 {
		return asUser == m.user
	}
	return slices.Contains(rule.As, asUser)
}

// patternMatches reports whether command satisfies one rule's regex
// patterns 1-for-1 (shape only, no `as:` check).
func patternMatches(command []string, rule *config.CommandRule) bool {
	if len(command) != len(rule.CommandPatterns) {
		return false
	}
	for i, s := range command {
		if !rule.CommandPatterns[i].MatchString(s) {
			return false
		}
	}
	return true
}
