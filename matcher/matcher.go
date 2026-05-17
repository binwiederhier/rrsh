package matcher

import (
	"strings"

	"github.com/binwiederhier/rrsh/config"
)

// shellMetachars are characters that indicate shell injection attempts.
const shellMetachars = "|;&$`>()<"

// Matcher checks input strings against a fixed set of CommandRules.
// It is safe to reuse a Matcher across many Match calls; the rules are
// not mutated.
type Matcher struct {
	rules []config.CommandRule
}

func New(rules []config.CommandRule) *Matcher {
	return &Matcher{rules: rules}
}

// Match returns the matching rule and true if input is allowed by any rule.
// Returns nil, false otherwise.
func (m *Matcher) Match(input string) (*config.CommandRule, bool) {
	if containsMetachars(input) {
		return nil, false
	}

	cmd, args := splitCommand(input)

	// Reject non-absolute paths
	if !strings.HasPrefix(cmd, "/") {
		return nil, false
	}

	for i := range m.rules {
		if m.rules[i].Path != cmd {
			continue
		}
		if m.rules[i].ArgsPattern == nil {
			return &m.rules[i], true
		}
		if m.rules[i].ArgsPattern.MatchString(args) {
			return &m.rules[i], true
		}
		return nil, false
	}

	return nil, false
}

// splitCommand splits input into the command and the remaining args string.
func splitCommand(input string) (string, string) {
	input = strings.TrimSpace(input)
	idx := strings.IndexByte(input, ' ')
	if idx == -1 {
		return input, ""
	}
	return input[:idx], input[idx+1:]
}

// containsMetachars returns true if input contains any shell metacharacters.
func containsMetachars(input string) bool {
	return strings.ContainsAny(input, shellMetachars)
}
