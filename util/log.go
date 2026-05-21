package util

import "strings"

// logEscaper neutralizes record-terminator chars (newline, CR, NUL) so
// an attacker can't forge fake ALLOWED/DENIED records in syslog by
// embedding them in argv elements.
var logEscaper = strings.NewReplacer("\n", "\\n", "\r", "\\r", "\x00", "\\0")

// JoinForLog formats (path, argv) as a single space-joined string for
// audit logging. Newlines, CRs, and NULs in any element are escaped
// (\n, \r, \0) so they cannot terminate the log record.
func JoinForLog(path string, argv []string) string {
	if len(argv) == 0 {
		return logEscaper.Replace(path)
	}
	escaped := make([]string, len(argv))
	for i, a := range argv {
		escaped[i] = logEscaper.Replace(a)
	}
	return logEscaper.Replace(path) + " " + strings.Join(escaped, " ")
}
