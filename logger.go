package main

import (
	"fmt"
	"log/syslog"
	"os"
	"os/user"
)

var syslogWriter *syslog.Writer

func initLogger() {
	w, err := syslog.New(syslog.LOG_AUTH|syslog.LOG_INFO, "noshell")
	if err != nil {
		fmt.Fprintf(os.Stderr, "noshell: warning: cannot open syslog: %v\n", err)
		return
	}
	syslogWriter = w
}

func logAllowed(input string) {
	msg := fmt.Sprintf("ALLOWED: user=%s cmd=%s", currentUser(), input)
	if syslogWriter != nil {
		syslogWriter.Info(msg)
	}
}

func logDenied(input string) {
	msg := fmt.Sprintf("DENIED: user=%s cmd=%s", currentUser(), input)
	if syslogWriter != nil {
		syslogWriter.Warning(msg)
	}
}

func currentUser() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}
