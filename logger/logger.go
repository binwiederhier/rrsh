package logger

import (
	"fmt"
	"log/syslog"
	"os"
	"strings"

	"github.com/binwiederhier/rrsh/util"
)

// SyslogLogger writes ALLOWED/DENIED events to the system auth log.
// A nil writer is tolerated (syslog open failed at startup) so the
// process keeps working even when no syslog daemon is reachable.
type SyslogLogger struct {
	w    *syslog.Writer
	user string
}

// New opens a connection to the local syslog daemon under facility
// auth/info with tag "rrsh". The username is supplied by the caller -
// the logger deliberately does not perform its own user lookup so the
// trust boundary stays at the entry-point (cmd/) layer. When the syslog
// open fails (no syslog daemon running, sandboxed container, etc.), New
// still returns a usable logger whose write methods become no-ops.
func New(username string) *SyslogLogger {
	w, err := syslog.New(syslog.LOG_AUTH|syslog.LOG_INFO, "rrsh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: warning: cannot open syslog: %v\n", err)
		w = nil
	}
	return &SyslogLogger{w: w, user: username}
}

// Allowed records a permitted command. asUser is the user the command will
// actually run as (equal to the SSH user for un-elevated commands, "root" or
// another user for elevated ones). For pipelines with mixed elevation, the
// caller can pass a comma-joined list (e.g. "self,root") so the audit line
// still surfaces that elevation happened.
func (l *SyslogLogger) Allowed(input, currentUser string) {
	l.AllowedFrom(input, currentUser, "")
}

// Denied records a rejected command. asUser is the user the caller asked to
// run as (or the current user when no elevation was requested).
func (l *SyslogLogger) Denied(input, currentUser string) {
	l.DeniedFrom(input, currentUser, "")
}

// AllowedFrom records a permitted command and additionally records the
// origin user (the SUDO_USER who triggered elevation). Used by the
// privileged half so the root-side audit line can be tied back to the
// originating SSH user without timestamp correlation.
func (l *SyslogLogger) AllowedFrom(input, currentUser, originUser string) {
	if l.w == nil {
		return
	}
	l.w.Info(formatEvent("ALLOWED", l.user, currentUser, originUser, input))
}

// DeniedFrom is the denial counterpart of AllowedFrom.
func (l *SyslogLogger) DeniedFrom(input, currentUser, originUser string) {
	if l.w == nil {
		return
	}
	l.w.Warning(formatEvent("DENIED", l.user, currentUser, originUser, input))
}

func (l *SyslogLogger) Close() error {
	if l.w == nil {
		return nil
	}
	return l.w.Close()
}

// formatEvent renders one syslog line. The as= and origin= fields are
// omitted when they equal user, keeping the common no-elevation case
// uncluttered while elevated calls stand out. All identity fields are
// escaped because an authenticated client controls the `as:` field
// pre-validation and could otherwise embed `\n ALLOWED: ...` to forge
// records; the command input is already escaped by util.JoinForLog at
// the call site.
func formatEvent(kind, user, asUser, origin, input string) string {
	user = util.EscapeForLog(user)
	asUser = util.EscapeForLog(asUser)
	origin = util.EscapeForLog(origin)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: user=%s", kind, user)
	if asUser != "" && asUser != user {
		fmt.Fprintf(&b, " as=%s", asUser)
	}
	if origin != "" && origin != user {
		fmt.Fprintf(&b, " origin=%s", origin)
	}
	fmt.Fprintf(&b, " cmd=%s", input)
	return b.String()
}
