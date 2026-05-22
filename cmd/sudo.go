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

	// Read current user, may be "root" or "deploy" (e.g. sudo -u deploy)
	u, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: cannot determine current user: %v\n", err)
		os.Exit(exitGeneric)
	}
	currentUser := u.Username

	// Ensure that we're calling via "sudo"
	originUser := os.Getenv("SUDO_USER")
	if originUser == "" {
		fmt.Fprintln(os.Stderr, "rrsh: sudo: must be invoked via /usr/bin/sudo (SUDO_USER unset)")
		os.Exit(exitDenied)
	}

	log := logger.New(currentUser)
	defer log.Close()

	// Load config
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	// Read command and arguments
	path, argv := args[0], args[1:]
	input := util.JoinForLog(path, argv)

	// Check if the command is allowed
	rule, ok := matcher.New(cfg.Commands).Match(path, argv)
	if !ok {
		log.DeniedFrom(input, currentUser, originUser)
		fmt.Fprintf(os.Stderr, "rrsh: command not allowed: %s\n", input)
		os.Exit(exitDenied)
	}

	// Authorize: us (currentUser) must be in the rule's `as:` list, with
	// "self" resolving to the originating SSH user (originUser).
	if err := auth.Check(currentUser, auth.Resolve(rule.As, originUser)); err != nil {
		log.DeniedFrom(input, currentUser, originUser)
		fmt.Fprintf(os.Stderr, "rrsh: %s not permitted to run as %s\n", input, currentUser)
		os.Exit(exitDenied)
	}

	log.AllowedFrom(input, currentUser, originUser)
	res := exec.Execute(path, argv, rule, os.Stdin)
	// Forward captured streams to our stdio so the parent's executor
	// in the unprivileged half sees them on its pipe.
	os.Stdout.Write(res.Stdout)
	os.Stderr.Write(res.Stderr)
	os.Exit(res.ExitCode)
}
