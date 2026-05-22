package util

import (
	"fmt"
	"strings"
)

// EscapeForLog neutralizes bytes that would let an authenticated caller
// forge fake ALLOWED/DENIED records in syslog or spoof terminal output
// when an operator views the audit log: record-terminator bytes
// (newline, CR, NUL) get readable escapes (\n, \r, \0), other C0
// controls and DEL become \xHH, and tab passes through as-is (harmless
// in syslog and useful in operator-authored input).
func EscapeForLog(s string) string {
	if !strings.ContainsFunc(s, needsLogEscape) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case 0x00:
			b.WriteString(`\0`)
		case '\t':
			b.WriteRune(r)
		default:
			if r < 0x20 || r == 0x7F {
				fmt.Fprintf(&b, `\x%02x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func needsLogEscape(r rune) bool {
	if r == '\t' {
		return false
	}
	return r < 0x20 || r == 0x7F
}

// JoinForLog formats (path, argv) as a single space-joined string for
// audit logging, with each element escaped via EscapeForLog so embedded
// control bytes cannot terminate the log record or smuggle terminal
// control sequences past an operator reading the log.
func JoinForLog(path string, argv []string) string {
	if len(argv) == 0 {
		return EscapeForLog(path)
	}
	escaped := make([]string, len(argv))
	for i, a := range argv {
		escaped[i] = EscapeForLog(a)
	}
	return EscapeForLog(path) + " " + strings.Join(escaped, " ")
}
