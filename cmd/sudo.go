package cmd

import (
	"fmt"
	"os"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/exec"
	"github.com/binwiederhier/rrsh/matcher"
	"github.com/binwiederhier/rrsh/util"
)

// runSudo is the privileged half of rrsh's elevation flow, invoked as
//
//	/usr/bin/sudo [-u USER] /usr/bin/rrsh sudo <path> <argv...>
//
// where sudoers grants `<ssh-user> ALL=(<targets>) NOPASSWD: /usr/bin/rrsh sudo *`.
// A parser bug here is a root compromise, so this subcommand refuses
// caller-controlled state: hardcoded config path, no flag parsing, and
// the matcher (built fresh from disk) authorizes both the command
// pattern and the rule's `as:` list against the post-sudo identity.
func runSudo(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "rrsh: sudo: missing command")
		os.Exit(exitDenied)
	}

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	// Build the matcher (auto-detects current OS user)
	m, err := matcher.New(cfg.Commands)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	// Match + authorize against the matcher's current user (root or
	// whatever sudo elevated to)
	rule, ok := m.Match(args)
	if !ok {
		fmt.Fprintf(os.Stderr, "rrsh: command not allowed: %s\n", util.JoinForLog(args))
		os.Exit(exitDenied)
	}

	// Run it
	res := exec.Execute(args, rule.Timeout, os.Stdin)
	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	os.Exit(res.ExitCode)
}
