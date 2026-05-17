package cmd

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/executor"
	"github.com/binwiederhier/rrsh/logger"
	"github.com/binwiederhier/rrsh/matcher"
)

// sudoMain is the privileged half of rrsh's elevation flow. It is invoked
// by the unprivileged rrsh process as `/usr/bin/sudo [-u USER] rrsh sudo <cmd>`,
// where sudoers grants `<ssh-user> ALL=(<targets>) NOPASSWD: /usr/bin/rrsh sudo *`.
//
// This subcommand sits on the root trust boundary: its caller is the
// unprivileged SSH user, and a parser bug here is a root compromise. It
// therefore refuses to honor any caller-controlled state — config path is
// hardcoded, no flag parsing, and the rule's `as` list is re-validated from
// disk against the effective euid before executing anything.
func sudoMain(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "rrsh: sudo: missing command")
		os.Exit(exitDenied)
	}

	log := logger.New()
	defer log.Close()

	// Hardcoded path — we are root (or another target user). The caller is
	// untrusted, so we must not read --config or $RRSH_CONFIG here.
	cfg, err := config.Load(defaultConfigPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	input := strings.Join(args, " ")

	me := currentUsername()
	origin := os.Getenv("SUDO_USER")
	if origin == "" {
		// Called directly without /usr/bin/sudo in front. No actual
		// elevation happened; `me` is also the origin. Still safe — we'll
		// only execute if `me` is in the rule's resolved as: list.
		origin = me
	}

	rule, ok := matcher.New(cfg.Commands).Match(input)
	if !ok {
		log.Denied(input, me)
		fmt.Fprintf(os.Stderr, "rrsh: command not allowed: %s\n", input)
		os.Exit(exitDenied)
	}

	allowed := resolveAllowedUsers(rule.As, origin)
	if !contains(allowed, me) {
		log.Denied(input, me)
		fmt.Fprintf(os.Stderr, "rrsh: %s not permitted to run as %s\n", input, me)
		os.Exit(exitDenied)
	}

	log.Allowed(input, me)
	os.Exit(executor.New(cfg.Timeout).Execute(input, rule))
}

func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	return u.Username
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
