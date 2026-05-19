package cmd

import (
	"github.com/binwiederhier/rrsh/config"
)

// resolveTarget decides which user the command should actually run as,
// given the requested target, the rule's `as:` list, and the SSH user
// (what "self" resolves to). Returns "" when the request must be denied.
//
// Rules:
//  1. If `requested` (with "self" resolved) is in the allowed list, use it.
//  2. If the caller didn't ask for elevation and `self` isn't allowed but
//     the rule lists exactly one user, implicitly elevate to that user —
//     the convenience for "always root" rules like systemctl restart …
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

// resolveAllowedUsers replaces every "self" token in the rule's as: list
// with the actual SSH username, leaving real usernames untouched.
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
