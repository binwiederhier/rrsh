package cmd

import (
	"strings"

	"github.com/pheckel/noshell/config"
)

// parseSudoPrefix strips an optional leading `sudo` (or `sudo -u USER`) sigil
// from the input. The returned target is the user the caller wants the command
// to run as: "self" if no prefix, "root" for bare `sudo`, or the username
// after `-u`. The returned rest is the remaining command line.
//
// Note: `sudo` here is a noshell-internal keyword, not a call to /usr/bin/sudo.
// noshell uses /usr/bin/sudo internally to perform the actual privilege
// transition (see cmd/run.go's elevate path), but the user-facing syntax is
// intentionally the same as real sudo so it's familiar.
func parseSudoPrefix(input string) (target, rest string) {
	trimmed := strings.TrimLeft(input, " ")
	if trimmed != "sudo" && !strings.HasPrefix(trimmed, "sudo ") {
		return config.SelfUser, input
	}
	after := strings.TrimLeft(strings.TrimPrefix(trimmed, "sudo"), " ")
	if !strings.HasPrefix(after, "-u ") && after != "-u" {
		return "root", after
	}
	afterFlag := strings.TrimLeft(strings.TrimPrefix(after, "-u"), " ")
	idx := strings.IndexByte(afterFlag, ' ')
	if idx == -1 {
		return afterFlag, ""
	}
	return afterFlag[:idx], strings.TrimLeft(afterFlag[idx+1:], " ")
}

// resolveTarget decides which user the command should actually run as, given
// the requested target (from parseSudoPrefix), the rule's `as:` list, and the
// SSH user (what "self" resolves to). Returns "" when the request must be
// denied.
//
// Rules:
//  1. If `requested` (with "self" resolved) is in the allowed list, use it.
//  2. If the caller didn't ask for elevation and `self` isn't allowed but the
//     rule lists exactly one user, implicitly elevate to that user — this is
//     the convenience for "always root" commands like `systemctl restart …`.
//  3. Otherwise, deny.
func resolveTarget(requested string, allowed []string, self string) string {
	if requested == config.SelfUser {
		requested = self
	}
	resolved := resolveAllowedUsers(allowed, self)
	for _, u := range resolved {
		if u == requested {
			return requested
		}
	}
	if requested == self && len(resolved) == 1 {
		return resolved[0]
	}
	return ""
}

// resolveAllowedUsers replaces every "self" token in the rule's as: list with
// the actual SSH username, leaving real usernames untouched.
func resolveAllowedUsers(allowed []string, self string) []string {
	out := make([]string, 0, len(allowed))
	for _, u := range allowed {
		if u == config.SelfUser {
			out = append(out, self)
		} else {
			out = append(out, u)
		}
	}
	return out
}
