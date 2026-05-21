package matcher

import (
	"regexp"
	"testing"

	"github.com/binwiederhier/rrsh/config"
)

// rulePatterns is a tiny constructor helper: builds a CommandRule with
// each regex source auto-anchored, matching what config.convertRule
// would produce in production.
func rulePatterns(path string, patterns ...string) config.CommandRule {
	compiled := make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		compiled[i] = regexp.MustCompile("^(?:" + p + ")$")
	}
	return config.CommandRule{
		Path:         path,
		ArgsPatterns: compiled,
		ArgsSource:   append([]string(nil), patterns...),
	}
}

func testMatcher(t *testing.T) *Matcher {
	t.Helper()
	return New([]config.CommandRule{
		{Path: "/usr/bin/whoami"},
		rulePatterns("/usr/bin/ls", "-la", "/var/log/.*"),
		rulePatterns("/usr/bin/ps", "aux"),  // first ps shape
		rulePatterns("/usr/bin/ps", "-ef"),  // second ps shape
		{Path: "/usr/bin/df"},
		{Path: "/usr/bin/grep"},
	})
}

func TestMatch_AllowedNoArgs(t *testing.T) {
	t.Parallel()
	rule, ok := testMatcher(t).Match("/usr/bin/whoami", nil)
	if !ok || rule.Path != "/usr/bin/whoami" {
		t.Error("whoami should be allowed")
	}
}

func TestMatch_AllowedWithArgs(t *testing.T) {
	t.Parallel()
	rule, ok := testMatcher(t).Match("/usr/bin/ls", []string{"-la", "/var/log/syslog"})
	if !ok || rule.Path != "/usr/bin/ls" {
		t.Error("ls -la /var/log/syslog should be allowed")
	}
}

func TestMatch_AllowedNoRestrictionWithArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/df", []string{"-h"})
	if !ok {
		t.Error("df -h should be allowed (no args restriction)")
	}
}

func TestMatch_DeniedWrongArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/ls", []string{"-la", "/etc/passwd"})
	if ok {
		t.Error("ls -la /etc/passwd should be denied")
	}
}

func TestMatch_DeniedUnknownCommand(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/rm", []string{"-rf", "/"})
	if ok {
		t.Error("rm should be denied")
	}
}

func TestMatch_DeniedRelativePath(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("whoami", nil)
	if ok {
		t.Error("relative path should be denied")
	}
}

// Metacharacters in argv element values are no longer a parser concern:
// argv arrives as a slice, so a "|" or ";" inside an element is just a
// byte. Only the rule's regex decides whether the value is allowed.
func TestMatch_MetacharsInArgvElementAreLiteralBytes(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	// /usr/bin/grep has no ArgsPatterns, so any argv shape is allowed
	// including a single element containing pipe / redirect characters.
	if _, ok := m.Match("/usr/bin/grep", []string{" | > /dev/null"}); !ok {
		t.Error("grep with quoted metachar arg should be allowed (no args constraint)")
	}
}

// Two rules with the same path describe alternative argv shapes; either
// one is enough to allow the call.
func TestMatch_MultipleRulesSamePath(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	if _, ok := m.Match("/usr/bin/ps", []string{"aux"}); !ok {
		t.Error("ps aux should match the first rule")
	}

	if _, ok := m.Match("/usr/bin/ps", []string{"-ef"}); !ok {
		t.Error("ps -ef should match the second rule (matcher must try both)")
	}

	if _, ok := m.Match("/usr/bin/ps", []string{"-aux", "--sort"}); ok {
		t.Error("ps -aux --sort matches neither shape, should be denied")
	}
}

// argv length and pattern-list length must match exactly. ["a","b"] does
// not match a one-element pattern even if joined-with-space would have.
func TestMatch_ArgvLengthMustMatch(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	// Rule for /usr/bin/ls expects exactly 2 elements.
	if _, ok := m.Match("/usr/bin/ls", []string{"-la"}); ok {
		t.Error("ls with 1 arg should be denied (rule expects 2)")
	}
	if _, ok := m.Match("/usr/bin/ls", []string{"-la", "/var/log/syslog", "extra"}); ok {
		t.Error("ls with 3 args should be denied (rule expects 2)")
	}
}

// The fix-for-#4 case: ["foo bar"] (one element with embedded space) is
// NOT the same as ["foo","bar"] (two elements). The matcher counts
// elements separately so a rule for `aux` (one element) doesn't accept
// `["a","ux"]` and a rule for `-la <path>` (two elements) doesn't accept
// `["-la /etc/passwd"]` (one element).
func TestMatch_JoinAmbiguityDefeated(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	// Rule expects two elements: ["-la", "/var/log/..."]. A single
	// element "/-la /var/log/syslog" must NOT match.
	if _, ok := m.Match("/usr/bin/ls", []string{"-la /var/log/syslog"}); ok {
		t.Error("single argv element joined-with-space must not satisfy a two-element pattern")
	}

	// Rule expects single element "aux". Two elements ["a", "ux"] must
	// not satisfy it.
	if _, ok := m.Match("/usr/bin/ps", []string{"a", "ux"}); ok {
		t.Error("two-element argv must not satisfy a one-element pattern")
	}
}

func TestMatch_EmptyPath(t *testing.T) {
	t.Parallel()
	if _, ok := testMatcher(t).Match("", nil); ok {
		t.Error("empty path should not match")
	}
}
