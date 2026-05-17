package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pheckel/noshell/config"
	"github.com/pheckel/noshell/executor"
	"github.com/pheckel/noshell/logger"
	"github.com/pheckel/noshell/matcher"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	exitDenied  = 126
	exitGeneric = 1

	sudoBinary = "/usr/bin/sudo"
)

var runCmd = &cobra.Command{
	Use:   "run [-c <command> | -- <command> [args...]]",
	Short: "Run a command against the allowlist (default when sshd invokes noshell -c ...)",
	Long: `Looks up the given command in the configured allowlist and either runs it
(logging an ALLOWED event to syslog) or denies it (logging DENIED and exiting 126).

Commands may optionally be prefixed with "sudo" or "sudo -u USER" to elect a
different target user; the rule's `+"`as:`"+` list controls which targets are
permitted. With no command, prints the allowlist and exits 0.

The root noshell command delegates here when invoked with -c or positional
args, so sshd can keep calling "noshell -c <command>" unchanged.`,
	RunE: runE,
}

func init() {
	rootCmd.AddCommand(runCmd)
	runCmd.Flags().StringVarP(&commandFlag, "command", "c", "", "command to run (shell mode, used by sshd)")
}

func runE(_ *cobra.Command, args []string) error {
	log := logger.New()
	defer log.Close()

	cfg, err := config.Load(viper.ConfigFileUsed())
	if err != nil {
		return err
	}

	input := resolveInput(args)
	if input == "" {
		printAllowed(cfg)
		return nil
	}

	requested, bare := parseSudoPrefix(input)
	if bare == "" {
		fmt.Fprintf(os.Stderr, "noshell: missing command after sudo\n")
		os.Exit(exitDenied)
	}

	self := currentUsername()

	rule, ok := matcher.New(cfg.Commands).Match(bare)
	if !ok {
		log.Denied(input, self)
		fmt.Fprintf(os.Stderr, "noshell: command not allowed: %s\n", input)
		os.Exit(exitDenied)
	}

	target := resolveTarget(requested, rule.As, self)
	if target == "" {
		log.Denied(input, self)
		fmt.Fprintf(os.Stderr, "noshell: %s not permitted to run as %s\n", bare, displayTarget(requested, self))
		os.Exit(exitDenied)
	}

	if target == self {
		log.Allowed(input, self)
		os.Exit(executor.New(cfg.Timeout).Execute(bare, rule))
	}

	log.Allowed(input, target)
	os.Exit(elevate(target, bare, rule, cfg.Timeout))
	return nil
}

// elevate re-invokes the noshell binary as `target` via /usr/bin/sudo. The
// privileged process (cmd/sudo.go) re-validates the command from disk before
// executing — this side cannot be trusted by it. Timeout handling and exit-
// code propagation are reused from the executor by treating the entire
// `sudo ... noshell sudo ...` invocation as a single command.
func elevate(target, bare string, rule *config.CommandRule, globalTimeout time.Duration) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "noshell: %v\n", err)
		return exitGeneric
	}
	var prefix string
	if target == "root" {
		prefix = fmt.Sprintf("%s -n %s sudo", sudoBinary, self)
	} else {
		prefix = fmt.Sprintf("%s -n -u %s %s sudo", sudoBinary, target, self)
	}
	return executor.New(globalTimeout).Execute(prefix+" "+bare, rule)
}

func resolveInput(args []string) string {
	if commandFlag != "" {
		return commandFlag
	}
	if len(args) > 0 {
		return strings.Join(args, " ")
	}
	return ""
}

func displayTarget(requested, self string) string {
	if requested == config.SelfUser {
		return self
	}
	return requested
}

func printAllowed(cfg *config.Config) {
	fmt.Println("Allowed commands:")
	for _, rule := range cfg.Commands {
		suffix := ""
		if !isDefaultAs(rule.As) {
			suffix = "  [as: " + strings.Join(rule.As, ", ") + "]"
		}
		if rule.ArgsPattern != nil {
			fmt.Printf("  %s %s%s\n", rule.Path, rule.ArgsPattern.String(), suffix)
		} else {
			fmt.Printf("  %s%s\n", rule.Path, suffix)
		}
	}
}

func isDefaultAs(as []string) bool {
	return len(as) == 1 && as[0] == config.SelfUser
}
