package auth

import (
	"errors"
	"slices"
)

// SelfUser is the magic token in a rule's "as:" list meaning "the SSH
// user who invoked rrsh"
const SelfUser = "self"

// ErrNotPermitted is returned by Check when the requested target user
// is not in the rule's allowed list.
var ErrNotPermitted = errors.New("requested user not permitted by rule's as: list")

// Check returns nil if requestedUser is in allowedUsers, or
// ErrNotPermitted otherwise. The list must already have been processed
// through Resolve so it contains no SelfUser sentinel values.
func Check(requestedUser string, allowedUsers []string) error {
	if slices.Contains(allowedUsers, requestedUser) {
		return nil
	}
	return ErrNotPermitted
}

// Resolve substitutes any SelfUser entries in allowedUsers with the
// concrete selfUser (the originating SSH identity) and deduplicates
func Resolve(allowedUsers []string, selfUser string) []string {
	seen := make(map[string]struct{}, len(allowedUsers))
	for _, u := range allowedUsers {
		if u == SelfUser {
			u = selfUser
		}
		seen[u] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for u := range seen {
		out = append(out, u)
	}
	return out
}
