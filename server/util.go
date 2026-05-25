package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/binwiederhier/rrsh/exec"
	"github.com/binwiederhier/rrsh/util"
)

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

// denyForCommand keeps the deny message in sync with the audit log
// format: same command rendering, same surrounding wording.
func denyForCommand(user string, command []string) *jsonrpcError {
	return deny("command not allowed for user " + user + ": " + util.JoinForLog(command))
}

// decodeParams strictly decodes a JSON-RPC `params` payload into dst.
// Empty payload and unknown fields both yield errInvalidParams.
func decodeParams[T any](method string, raw json.RawMessage, dst *T) *jsonrpcError {
	if len(raw) == 0 {
		return &jsonrpcError{Code: errInvalidParams, Message: method + " requires params"}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return &jsonrpcError{Code: errInvalidParams, Message: "invalid " + method + " params: " + err.Error()}
	}
	return nil
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

// sanitizeDescription strips C0+DEL from operator-authored strings so
// stray ESC/BEL can't become terminal injection in the client. Tab and
// newline survive so multi-line text still renders.
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

// safeUTF8 replaces invalid UTF-8 with U+FFFD so arbitrary subprocess
// output can be marshaled as JSON.
func safeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// formatStagesForLog renders the pipeline's exec form (sudo-wrapping
// included) as one syslog string, with " | " between stages.
func formatStagesForLog(stages []*exec.Stage) string {
	parts := make([]string, len(stages))
	for i, s := range stages {
		if len(s.Command) == 0 {
			parts[i] = ""
			continue
		}
		parts[i] = util.JoinForLog(s.Command)
	}
	return strings.Join(parts, " | ")
}
