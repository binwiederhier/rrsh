// Package util holds small, dependency-free helpers shared across rrsh
// packages. Anything that needs imports beyond the Go stdlib does not
// belong here — keeping util tiny is what justifies its existence in a
// trust-sensitive codebase.
package util

import "os/user"

// UnknownUser is the placeholder returned by CurrentUser when the OS
// lookup fails. Exported so callers can branch on it without retyping
// the magic string.
const UnknownUser = "unknown"

// CurrentUser returns the username of the process's effective user, or
// UnknownUser if the lookup fails. The fallback keeps downstream
// formatting (syslog tags, error messages, etc.) from having to handle
// nil/empty values themselves.
func CurrentUser() string {
	u, err := user.Current()
	if err != nil {
		return UnknownUser
	}
	return u.Username
}
