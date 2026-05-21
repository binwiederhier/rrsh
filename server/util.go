package server

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/exec"
	"github.com/binwiederhier/rrsh/util"
)

// errUserNotPermitted is returned by resolveUser when the requested
// target user is not allowed by the matched rule's `as:` list.
var errUserNotPermitted = errors.New("requested user not permitted by rule's as: list")

// toRunResult converts the executor's internal Result into the wire shape.
func toRunResult(res *exec.Result) *runResult {
	return &runResult{
		Stdout:    safeUTF8(res.Stdout),
		Stderr:    safeUTF8(res.Stderr),
		Exit:      res.ExitCode,
		TimedOut:  res.TimedOut,
		Truncated: res.Truncated,
	}
}

// deny builds the application-specific "denied" RPC error.
func deny(msg string) *jsonrpcError {
	return &jsonrpcError{Code: errDenied, Message: msg}
}

// errResponse wraps a JSON-RPC error into a full response envelope. A
// nil id is rendered as JSON null per the spec.
func errResponse(id json.RawMessage, code int, msg string) *jsonrpcResponse {
	if id == nil {
		id = json.RawMessage("null")
	}
	return &jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonrpcError{Code: code, Message: msg},
	}
}

// sanitizeDescription strips C0 controls + DEL from operator-authored
// descriptions before hello.commands returns them - keeps stray ESC or
// BEL from becoming terminal-injection in the AI client's UI. Tab and
// newline survive so multi-line descriptions still render.
func sanitizeDescription(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		} else if r < 0x20 || r == 0x7F {
			return -1 // drop
		}
		return r
	}, s)
}

// safeUTF8 replaces invalid UTF-8 with U+FFFD so arbitrary command
// output (binary data, stray escapes) can be marshaled as JSON.
func safeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// resolveUser returns the effective user a call should run as, or
// errUserNotPermitted if the requested user is not in the rule's
// allowed list. "self" in requested or in the allowed list resolves to
// the SSH user. A single-user rule implicitly elevates when the caller
// didn't ask for a different user (the common "always root" case).
func resolveUser(currentUser, requestedUser string, allowed []string) (string, error) {
	if requestedUser == config.SelfUser {
		requestedUser = currentUser
	}
	var single string
	for _, u := range allowed {
		if u == config.SelfUser {
			u = currentUser
		}
		if u == requestedUser {
			return requestedUser, nil
		}
		single = u
	}
	if requestedUser == currentUser && len(allowed) == 1 {
		return single, nil
	}
	return "", errUserNotPermitted
}

// displayUser returns the user name to put in an error message, with
// "self" replaced by the actual SSH user for readability.
func displayUser(requested, self string) string {
	if requested == config.SelfUser {
		return self
	}
	return requested
}

// formatPipelineLog formats a pipeline as a single space-joined string for
// syslog. Stages are joined with " | " for readability.
func formatPipelineLog(stages []*runStep) string {
	parts := make([]string, len(stages))
	for i, st := range stages {
		path := ""
		var rest []string
		if len(st.Argv) > 0 {
			path = st.Argv[0]
			rest = st.Argv[1:]
		}
		parts[i] = util.JoinForLog(path, rest)
	}
	return strings.Join(parts, " | ")
}
