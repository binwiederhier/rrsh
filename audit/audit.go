// Package audit emits ALLOWED/DENIED records to the system auth log.
package audit

import (
	"fmt"
	"log/syslog"
	"os"

	"github.com/binwiederhier/rrsh/util"
)

// Logger writes ALLOWED/DENIED events to the system auth log. A nil
// writer makes writes no-ops so the process survives a missing syslog
// daemon.
type Logger struct {
	w *syslog.Writer
}

// New opens an auth/info syslog connection tagged "rrsh". On open
// failure the returned Logger's writes are no-ops.
func New() *Logger {
	w, err := syslog.New(syslog.LOG_AUTH|syslog.LOG_INFO, "rrsh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: warning: cannot open syslog: %v\n", err)
		w = nil
	}
	return &Logger{w: w}
}

// Allowed records a permitted command. user is the SSH user.
func (l *Logger) Allowed(cmd, user string) {
	if l.w == nil {
		return
	}
	l.w.Info(formatEvent("ALLOWED", user, cmd))
}

// Denied records a rejected command. user is the SSH user.
func (l *Logger) Denied(cmd, user string) {
	if l.w == nil {
		return
	}
	l.w.Warning(formatEvent("DENIED", user, cmd))
}

// Close releases the syslog connection.
func (l *Logger) Close() error {
	if l.w == nil {
		return nil
	}
	return l.w.Close()
}

// formatEvent renders one syslog line. user is escaped as
// defense-in-depth; cmd is already escaped by util.JoinForLog.
func formatEvent(kind, user, cmd string) string {
	return fmt.Sprintf("%s: user=%s cmd=%s", kind, util.EscapeForLog(user), cmd)
}
