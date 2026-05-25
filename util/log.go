package util

import (
	"fmt"
	"strings"
)

// EscapeForLog neutralizes bytes that could forge fake syslog records
// or spoof terminal output: \n/\r/\0 become readable escapes, other
// C0+DEL become \xHH, tab passes through.
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

// JoinForLog space-joins command with each element run through
// EscapeForLog, safe to drop into a syslog record.
func JoinForLog(command []string) string {
	escaped := make([]string, len(command))
	for i, s := range command {
		escaped[i] = EscapeForLog(s)
	}
	return strings.Join(escaped, " ")
}
