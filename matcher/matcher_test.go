package matcher

import (
	"regexp"
	"testing"

	"github.com/binwiederhier/rrsh/auth"
	"github.com/binwiederhier/rrsh/config"
)

// makeRule mirrors what config.convertRule produces: every operator-authored
// regex wrapped in ^(?:...)$, As defaulting to [auth.SelfUser].
func makeRule(command ...string) config.CommandRule {
	compiled := make([]*regexp.Regexp, len(command))
	for i, p := range command {
		compiled[i] = regexp.MustCompile("^(?:" + p + ")$")
	}
	return config.CommandRule{
		CommandPatterns: compiled,
		CommandSource:   append([]string(nil), command...),
		As:              []string{auth.SelfUser},
	}
}

func testMatcher(t *testing.T) *Matcher {
	t.Helper()
	return NewForUser([]config.CommandRule{
		makeRule("/usr/bin/whoami"),
		makeRule("/usr/bin/ls", "-la", "/var/log/.*"),
		makeRule("/usr/bin/ps", "aux"),
		makeRule("/usr/bin/ps", "-ef"),
		makeRule("/usr/bin/df", ".*"), // any single argv element
		makeRule("/usr/bin/df"),       // also zero args
		makeRule("/usr/bin/grep", ".*"),
	}, "tester")
}

func TestMatch_AllowedNoArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match([]string{"/usr/bin/whoami"})
	if !ok {
		t.Error("whoami should be allowed")
	}
}

func TestMatch_AllowedWithArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match([]string{"/usr/bin/ls", "-la", "/var/log/syslog"})
	if !ok {
		t.Error("ls -la /var/log/syslog should be allowed")
	}
}

func TestMatch_AllowedNoArgsVariant(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match([]string{"/usr/bin/df"})
	if !ok {
		t.Error("df with no args should be allowed (zero-args rule)")
	}
}

func TestMatch_AllowedSingleArg(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match([]string{"/usr/bin/df", "-h"})
	if !ok {
		t.Error("df -h should be allowed (one-arg variant)")
	}
}

func TestMatch_DeniedWrongArgs(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match([]string{"/usr/bin/ls", "-la", "/etc/passwd"})
	if ok {
		t.Error("ls -la /etc/passwd should be denied")
	}
}

func TestMatch_DeniedUnknownCommand(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match([]string{"/usr/bin/rm", "-rf", "/"})
	if ok {
		t.Error("rm should be denied")
	}
}

func TestMatch_DeniedRelativePath(t *testing.T) {
	t.Parallel()
	_, ok := testMatcher(t).Match([]string{"whoami"})
	if ok {
		t.Error("relative path should be denied")
	}
}

// Metacharacters in argv element values are no longer a parser concern.
func TestMatch_MetacharsInArgvElementAreLiteralBytes(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)
	if _, ok := m.Match([]string{"/usr/bin/grep", " | > /dev/null"}); !ok {
		t.Error("grep with quoted metachar arg should be allowed by `.*` element regex")
	}
}

// Two rules with the same command[0] describe alternative argv shapes.
func TestMatch_MultipleRulesSamePath(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	if _, ok := m.Match([]string{"/usr/bin/ps", "aux"}); !ok {
		t.Error("ps aux should match the first ps rule")
	}
	if _, ok := m.Match([]string{"/usr/bin/ps", "-ef"}); !ok {
		t.Error("ps -ef should match the second ps rule (matcher must try both)")
	}
	if _, ok := m.Match([]string{"/usr/bin/ps", "-aux", "--sort"}); ok {
		t.Error("ps -aux --sort matches neither rule, should be denied")
	}
}

// argv length and pattern-list length must match exactly.
func TestMatch_ArgvLengthMustMatch(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	if _, ok := m.Match([]string{"/usr/bin/ls", "-la"}); ok {
		t.Error("ls with 1 arg should be denied (rule expects 2)")
	}
	if _, ok := m.Match([]string{"/usr/bin/ls", "-la", "/var/log/syslog", "extra"}); ok {
		t.Error("ls with 3 args should be denied (rule expects 2)")
	}
}

// The fix-for-#4 case: ["foo bar"] (one element with embedded space) is
// NOT the same as ["foo","bar"] (two elements).
func TestMatch_JoinAmbiguityDefeated(t *testing.T) {
	t.Parallel()
	m := testMatcher(t)

	if _, ok := m.Match([]string{"/usr/bin/ls", "-la /var/log/syslog"}); ok {
		t.Error("single argv element joined-with-space must not satisfy a two-element pattern")
	}
	if _, ok := m.Match([]string{"/usr/bin/ps", "a", "ux"}); ok {
		t.Error("two-element argv must not satisfy a one-element pattern")
	}
}

// command[0] is itself a regex - a rule can match multiple binaries.
func TestMatch_CommandZeroIsRegex(t *testing.T) {
	t.Parallel()
	m := NewForUser([]config.CommandRule{
		makeRule("/usr/bin/(cat|head)", "/etc/hostname"),
	}, "tester")
	if _, ok := m.Match([]string{"/usr/bin/cat", "/etc/hostname"}); !ok {
		t.Error("/usr/bin/cat /etc/hostname should match (regex command[0])")
	}
	if _, ok := m.Match([]string{"/usr/bin/head", "/etc/hostname"}); !ok {
		t.Error("/usr/bin/head /etc/hostname should match (regex command[0])")
	}
	if _, ok := m.Match([]string{"/usr/bin/tail", "/etc/hostname"}); ok {
		t.Error("/usr/bin/tail must NOT match - outside the alternation")
	}
}

func TestMatch_EmptyCommand(t *testing.T) {
	t.Parallel()
	if _, ok := testMatcher(t).Match(nil); ok {
		t.Error("empty command should not match")
	}
}

// TestMatch_AuthDeniesWhenUserNotInAsList: a rule's `as:` list excluding
// the matcher's user causes the rule to be skipped even if the command
// pattern matches.
func TestMatch_AuthDeniesWhenUserNotInAsList(t *testing.T) {
	t.Parallel()
	rule := makeRule("/usr/bin/whoami")
	rule.As = []string{"root"} // not "tester"
	m := NewForUser([]config.CommandRule{rule}, "tester")
	if _, ok := m.Match([]string{"/usr/bin/whoami"}); ok {
		t.Error("rule's as=[root] should not authorize tester")
	}
}

// TestMatch_AuthAllowsWhenUserInAsList: explicit user in the as: list.
func TestMatch_AuthAllowsWhenUserInAsList(t *testing.T) {
	t.Parallel()
	rule := makeRule("/usr/bin/whoami")
	rule.As = []string{"root"}
	m := NewForUser([]config.CommandRule{rule}, "root")
	if _, ok := m.Match([]string{"/usr/bin/whoami"}); !ok {
		t.Error("rule's as=[root] should authorize root")
	}
}

// TestMatchAsUser_ElevationPath: matcher bound to SSH user, request
// asks for elevation to root - MatchAsUser("root") authorizes against
// the rule's as: list.
func TestMatchAsUser_ElevationPath(t *testing.T) {
	t.Parallel()
	rule := makeRule("/usr/bin/whoami")
	rule.As = []string{"root"}
	m := NewForUser([]config.CommandRule{rule}, "tester")

	// Bare Match (= MatchAsUser with "") authorizes the SSH user.
	if _, ok := m.Match([]string{"/usr/bin/whoami"}); ok {
		t.Error("rule's as=[root] should not authorize tester")
	}
	// MatchAsUser with explicit "root" target authorizes.
	if _, ok := m.MatchAsUser([]string{"/usr/bin/whoami"}, "root"); !ok {
		t.Error("MatchAsUser(root) should be allowed by rule's as=[root]")
	}
	// $USER and "" resolve to the matcher's user (tester).
	if _, ok := m.MatchAsUser([]string{"/usr/bin/whoami"}, auth.SelfUser); ok {
		t.Error("MatchAsUser($USER) should resolve to tester and be denied")
	}
	if _, ok := m.MatchAsUser([]string{"/usr/bin/whoami"}, ""); ok {
		t.Error("MatchAsUser(\"\") should resolve to tester and be denied")
	}
}
