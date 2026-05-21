package matcher

import (
	"regexp"
	"testing"

	"github.com/binwiederhier/rrsh/config"
)

// makeRule is a tiny constructor that mirrors what config.convertRule
// produces in production: every operator-authored regex is wrapped in
// ^(?:…)$ so MatchString matches the whole element.
func makeRule(command ...string) config.CommandRule {
	compiled := make([]*regexp.Regexp, len(command))
	for i, p := range command {
		compiled[i] = regexp.MustCompile("^(?:" + p + ")$")
	}
	return config.CommandRule{
		CommandPatterns: compiled,
		CommandSource:   append([]string(nil), command...),
	}
}

func testMatcher(t *testing.T) *Matcher {
	t.Helper()
	return New([]config.CommandRule{
		makeRule("/usr/bin/whoami"),
		makeRule("/usr/bin/ls", "-la", "/var/log/.*"),
		makeRule("/usr/bin/ps", "aux"),
		makeRule("/usr/bin/ps", "-ef"),
		makeRule("/usr/bin/df", ".*"), // any single argv element
		makeRule("/usr/bin/df"),       // also zero args
		makeRule("/usr/bin/grep", ".*"),
	})
}

func TestMatch_AllowedNoArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/whoami", nil)
	if !ok {
		t.Error("whoami should be allowed")
	}
}

func TestMatch_AllowedWithArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match("/usr/bin/ls", []string{"-la", "/var/log/syslog"})
	if !ok {
		t.Error("ls -la /var/log/syslog should be allowed")
	}
}

func TestMatch_AllowedNoArgsVariant(t *testing.T) {
	t.Parallel()
	// df with no argv matches the "zero-args" rule.
	_, ok := testMatcher(t).Match("/usr/bin/df", nil)
	if !ok {
		t.Error("df with no args should be allowed (zero-args rule)")
	}
}

func TestMatch_AllowedSingleArg(t *testing.T) {
	t.Parallel()
	// df -h matches the ".*" single-arg rule.
	_, ok := testMatcher(t).Match("/usr/bin/df", []string{"-h"})
	if !ok {
		t.Error("df -h should be allowed (one-arg variant)")
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

// Metacharacters in argv element values are no longer a parser concern.
func TestMatch_MetacharsInArgvElementAreLiteralBytes(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	// /usr/bin/grep accepts any single argv (rule has ".*"), including
	// one containing pipe/redirect characters.
	if _, ok := m.Match("/usr/bin/grep", []string{" | > /dev/null"}); !ok {
		t.Error("grep with quoted metachar arg should be allowed by `.*` element regex")
	}
}

// Two rules with the same command[0] describe alternative argv shapes.
func TestMatch_MultipleRulesSamePath(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	if _, ok := m.Match("/usr/bin/ps", []string{"aux"}); !ok {
		t.Error("ps aux should match the first ps rule")
	}
	if _, ok := m.Match("/usr/bin/ps", []string{"-ef"}); !ok {
		t.Error("ps -ef should match the second ps rule (matcher must try both)")
	}
	if _, ok := m.Match("/usr/bin/ps", []string{"-aux", "--sort"}); ok {
		t.Error("ps -aux --sort matches neither rule, should be denied")
	}
}

// argv length and pattern-list length must match exactly.
func TestMatch_ArgvLengthMustMatch(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	// Rule for /usr/bin/ls expects exactly 2 argv elements.
	if _, ok := m.Match("/usr/bin/ls", []string{"-la"}); ok {
		t.Error("ls with 1 arg should be denied (rule expects 2)")
	}
	if _, ok := m.Match("/usr/bin/ls", []string{"-la", "/var/log/syslog", "extra"}); ok {
		t.Error("ls with 3 args should be denied (rule expects 2)")
	}
}

// The fix-for-#4 case: ["foo bar"] (one element with embedded space) is
// NOT the same as ["foo","bar"] (two elements).
func TestMatch_JoinAmbiguityDefeated(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	// Rule expects 2 argv elements: ["-la", "/var/log/.*"]. A single
	// element "-la /var/log/syslog" must NOT match.
	if _, ok := m.Match("/usr/bin/ls", []string{"-la /var/log/syslog"}); ok {
		t.Error("single argv element joined-with-space must not satisfy a two-element pattern")
	}

	// Rule expects single element "aux". Two elements ["a", "ux"] must
	// not satisfy it.
	if _, ok := m.Match("/usr/bin/ps", []string{"a", "ux"}); ok {
		t.Error("two-element argv must not satisfy a one-element pattern")
	}
}

// command[0] is itself a regex - a rule can match multiple binaries.
func TestMatch_CommandZeroIsRegex(t *testing.T) {
	t.Parallel()
	m := New([]config.CommandRule{
		makeRule("/usr/bin/(cat|head)", "/etc/hostname"),
	})
	if _, ok := m.Match("/usr/bin/cat", []string{"/etc/hostname"}); !ok {
		t.Error("/usr/bin/cat /etc/hostname should match (regex command[0])")
	}
	if _, ok := m.Match("/usr/bin/head", []string{"/etc/hostname"}); !ok {
		t.Error("/usr/bin/head /etc/hostname should match (regex command[0])")
	}
	if _, ok := m.Match("/usr/bin/tail", []string{"/etc/hostname"}); ok {
		t.Error("/usr/bin/tail must NOT match - outside the alternation")
	}
}

func TestMatch_EmptyPath(t *testing.T) {
	t.Parallel()
	if _, ok := testMatcher(t).Match("", nil); ok {
		t.Error("empty path should not match")
	}
}
