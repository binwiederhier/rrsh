package cmd

import (
	"fmt"
	"os"
	"os/user"

	"github.com/binwiederhier/rrsh/auth"
	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/exec"
	"github.com/binwiederhier/rrsh/logger"
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
// the rule's `as:` list is re-validated from disk before exec.
func runSudo(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "rrsh: sudo: missing command")
		os.Exit(exitDenied)
	}

	// Read current user
	u, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: cannot determine current user: %v\n", err)
		os.Exit(exitGeneric)
	}
	currentUser := u.Username

	log := logger.New(currentUser)
	defer log.Close()

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	// Read command and arguments
	path := args[0]
	argv := args[1:]
	input := util.JoinForLog(path, argv)

	// origin = who asked for elevation. Falls back to me when invoked
	// without /usr/bin/sudo in front (no real elevation happened).
	sudoUser := os.Getenv("SUDO_USER")
	if sudoUser == "" {
		sudoUser = currentUser
	}

	rule, ok := matcher.New(cfg.Commands).Match(path, argv)
	if !ok {
		log.DeniedFrom(input, currentUser, sudoUser)
		fmt.Fprintf(os.Stderr, "rrsh: command not allowed: %s\n", input)
		os.Exit(exitDenied)
	}

	// Authorize: `me` (currentUser) must be in the rule's `as:` list, with
	// "self" resolving to the originating SSH user (sudoUser).
	if err := auth.Check(currentUser, sudoUser, rule.As); err != nil {
		log.DeniedFrom(input, currentUser, sudoUser)
		fmt.Fprintf(os.Stderr, "rrsh: %s not permitted to run as %s\n", input, currentUser)
		os.Exit(exitDenied)
	}

	log.AllowedFrom(input, currentUser, sudoUser)
	res := exec.Execute(path, argv, rule, os.Stdin)
	// Forward captured streams to our stdio so the parent's executor
	// in the unprivileged half sees them on its pipe.
	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	os.Exit(res.ExitCode)
}
