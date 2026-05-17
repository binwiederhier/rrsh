package cmd

import (
	"fmt"
	"os"
	"os/user"
	"strings"

	"github.com/pheckel/noshell/config"
	"github.com/pheckel/noshell/executor"
	"github.com/pheckel/noshell/logger"
	"github.com/pheckel/noshell/matcher"
	"github.com/spf13/cobra"
)

// sudoCmd is the privileged half of noshell's elevation flow. It is invoked by
// the unprivileged noshell process as `/usr/bin/sudo [-u USER] noshell sudo <cmd>`,
// where sudoers grants `<ssh-user> ALL=(<targets>) NOPASSWD: /usr/bin/noshell sudo *`.
//
// This subcommand sits on the root trust boundary: its caller is the
// unprivileged SSH user, and a parser bug here is a root compromise. It
// therefore refuses to honor any caller-controlled state — config path is
// hardcoded, --config is ignored, and the rule's `as:` list is re-validated
// from disk against the effective euid before executing anything.
var sudoCmd = &cobra.Command{
	Use:    "sudo <command> [args...]",
	Short:  "Internal: re-validate and run a command as the elevated user",
	Hidden: true,
	Args:   cobra.MinimumNArgs(1),
	RunE:   sudoE,
}

func init() {
	rootCmd.AddCommand(sudoCmd)
}

func sudoE(_ *cobra.Command, args []string) error {
	log := logger.New()
	defer log.Close()

	// Hardcoded path — we are root (or another target user). The caller is
	// untrusted, so we must not read --config or $NOSHELL_CONFIG here.
	cfg, err := config.Load(defaultConfigPath)
	if err != nil {
		return err
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
		fmt.Fprintf(os.Stderr, "noshell: command not allowed: %s\n", input)
		os.Exit(exitDenied)
	}

	allowed := resolveAllowedUsers(rule.As, origin)
	if !contains(allowed, me) {
		log.Denied(input, me)
		fmt.Fprintf(os.Stderr, "noshell: %s not permitted to run as %s\n", input, me)
		os.Exit(exitDenied)
	}

	log.Allowed(input, me)
	os.Exit(executor.New(cfg.Timeout).Execute(input, rule))
	return nil
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
