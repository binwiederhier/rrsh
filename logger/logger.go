package logger

import (
	"fmt"
	"log/syslog"
	"os"

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
// auth/info with tag "rrsh". When the open fails (no syslog daemon
// running, sandboxed container, etc.), New still returns a usable
// logger whose write methods become no-ops, so the calling process
// keeps working with logging downgraded to silent.
func New() *SyslogLogger {
	w, err := syslog.New(syslog.LOG_AUTH|syslog.LOG_INFO, "rrsh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: warning: cannot open syslog: %v\n", err)
		w = nil
	}
	return &SyslogLogger{w: w, user: util.CurrentUser()}
}

// Allowed records a permitted command. asUser is the user the command will
// actually run as (equal to the SSH user for un-elevated commands, "root" or
// another user for elevated ones).
func (l *SyslogLogger) Allowed(input, asUser string) {
	if l.w == nil {
		return
	}
	l.w.Info(formatEvent("ALLOWED", l.user, asUser, input))
}

// Denied records a rejected command. asUser is the user the caller asked to
// run as (or the current user when no elevation was requested).
func (l *SyslogLogger) Denied(input, asUser string) {
	if l.w == nil {
		return
	}
	l.w.Warning(formatEvent("DENIED", l.user, asUser, input))
}

func (l *SyslogLogger) Close() error {
	if l.w == nil {
		return nil
	}
	return l.w.Close()
}

// formatEvent omits the as= field when it equals the calling user — the
// common no-elevation case stays uncluttered while elevated calls stand out.
func formatEvent(kind, user, asUser, input string) string {
	if asUser == "" || asUser == user {
		return fmt.Sprintf("%s: user=%s cmd=%s", kind, user, input)
	}
	return fmt.Sprintf("%s: user=%s as=%s cmd=%s", kind, user, asUser, input)
}

