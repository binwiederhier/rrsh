package cmd

import (
	"fmt"
	"os"
	"strings"

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
)

var runCmd = &cobra.Command{
	Use:   "run [-c <command> | -- <command> [args...]]",
	Short: "Run a command against the allowlist (default when sshd invokes noshell -c ...)",
	Long: `Looks up the given command in the configured allowlist and either runs it
(logging an ALLOWED event to syslog) or denies it (logging DENIED and exiting 126).

With no command, prints the allowlist and exits 0. The root noshell command
delegates to this subcommand when invoked with -c or positional args, so sshd
can keep calling "noshell -c <command>" unchanged.`,
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

	rule, ok := matcher.New(cfg.Commands).Match(input)
	if !ok {
		log.Denied(input)
		fmt.Fprintf(os.Stderr, "noshell: command not allowed: %s\n", input)
		os.Exit(exitDenied)
	}

	log.Allowed(input)
	os.Exit(executor.New(cfg.Timeout).Execute(input, rule))
	return nil
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

func printAllowed(cfg *config.Config) {
	fmt.Println("Allowed commands:")
	for _, rule := range cfg.Commands {
		if rule.ArgsPattern != nil {
			fmt.Printf("  %s %s\n", rule.Path, rule.ArgsPattern.String())
		} else {
			fmt.Printf("  %s\n", rule.Path)
		}
	}
}
