package logger

import (
	"fmt"
	"log/syslog"
	"os"
	"os/user"
)

// SyslogLogger writes ALLOWED/DENIED events to the system auth log.
// A nil writer is tolerated (syslog open failed at startup) so the
// process keeps working even when no syslog daemon is reachable.
type SyslogLogger struct {
	w    *syslog.Writer
	user string
}

func New() *SyslogLogger {
	w, err := syslog.New(syslog.LOG_AUTH|syslog.LOG_INFO, "noshell")
	if err != nil {
		fmt.Fprintf(os.Stderr, "noshell: warning: cannot open syslog: %v\n", err)
		w = nil
	}
	return &SyslogLogger{w: w, user: currentUser()}
}

func (l *SyslogLogger) Allowed(input string) {
	if l.w == nil {
		return
	}
	l.w.Info(fmt.Sprintf("ALLOWED: user=%s cmd=%s", l.user, input))
}

func (l *SyslogLogger) Denied(input string) {
	if l.w == nil {
		return
	}
	l.w.Warning(fmt.Sprintf("DENIED: user=%s cmd=%s", l.user, input))
}

func (l *SyslogLogger) Close() error {
	if l.w == nil {
		return nil
	}
	return l.w.Close()
}

func currentUser() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}
