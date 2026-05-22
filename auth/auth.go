// Package auth holds the rule-level authorization check that is shared
// by the JSON-RPC server (server/) and the privileged half (cmd/sudo.go).
// Pulling it out of server/ lets cmd/sudo.go authorize without importing
// the JSON-RPC code path - the privileged binary's blast radius stays
// matcher+config+auth+exec only.
package auth

import "errors"

// SelfUser is the magic token in a rule's `as:` list meaning "the SSH
// user who invoked rrsh". Resolved at runtime against $SUDO_USER (for
// the privileged subcommand) or the current user (unprivileged).
const SelfUser = "self"

// ErrNotPermitted is returned by Check when the requested target user
// is not in the rule's allowed list.
var ErrNotPermitted = errors.New("requested user not permitted by rule's as: list")

// Check returns nil if requestedUser is in allowedUsers, with SelfUser
// entries resolved against selfUser before comparison. Both
// requestedUser and selfUser must already be normalized (no "" or
// SelfUser sentinel values).
func Check(requestedUser, selfUser string, allowedUsers []string) error {
	for _, u := range allowedUsers {
		if u == SelfUser {
			u = selfUser
		}
		if u == requestedUser {
			return nil
		}
	}
	return ErrNotPermitted
}
