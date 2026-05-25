package matcher

import (
	"fmt"
	"os/user"
	"strings"

	"github.com/binwiederhier/rrsh/auth"
	"github.com/binwiederhier/rrsh/config"
)

// Matcher resolves a command to its allowlist rule and authorizes the
// matcher's user against the rule's `as:` list. Safe to reuse across
// calls; rules are not mutated.
type Matcher struct {
	rules []config.CommandRule
	user  string
}

// New constructs a Matcher whose user is the OS process user (via
// user.Current). Used by the privileged half (cmd/sudo.go) where
// "current user" is whatever sudo elevated to.
func New(rules []config.CommandRule) (*Matcher, error) {
	u, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("matcher: cannot determine current user: %w", err)
	}
	return &Matcher{rules: rules, user: u.Username}, nil
}

// NewForUser constructs a Matcher bound to an explicit user. The
// JSON-RPC server uses this for un-elevated requests (bound to the
// SSH user) and for elevated requests (bound to the target user).
func NewForUser(rules []config.CommandRule, user string) *Matcher {
	return &Matcher{rules: rules, user: user}
}

// Match returns the first rule whose command pattern matches AND whose
// `as:` list authorizes the matcher's user. Shorthand for
// MatchAsUser(command, "").
func (m *Matcher) Match(command []string) (*config.CommandRule, bool) {
	return m.MatchAsUser(command, "")
}

// MatchAsUser returns the first rule whose command pattern matches AND
// whose `as:` list authorizes asUser. An empty asUser or auth.SelfUser
// resolves to the matcher's user; auth.SelfUser entries inside the
// rule's `as:` list resolve the same way. command[0] is the binary
// path; command[1:] is argv.
func (m *Matcher) MatchAsUser(command []string, asUser string) (*config.CommandRule, bool) {
	if asUser == "" || asUser == auth.SelfUser {
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

// authorized reports whether asUser is in rule.As, with auth.SelfUser
// substituted to the matcher's user.
func (m *Matcher) authorized(rule *config.CommandRule, asUser string) bool {
	for _, allowed := range rule.As {
		if allowed == auth.SelfUser {
			allowed = m.user
		}
		if allowed == asUser {
			return true
		}
	}
	return false
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
