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
	defaultConfigPath = "/etc/noshell/noshell.yml"
	envConfigPath     = "NOSHELL_CONFIG"

	exitDenied  = 126
	exitGeneric = 1
)

var (
	configFile  string
	commandFlag string
)

var rootCmd = &cobra.Command{
	Use:   "noshell [-c <command> | -- <command> [args...]]",
	Short: "Restricted login shell that allowlists commands via YAML",
	Long: `noshell is a restricted login shell. When set as a user's login shell,
sshd invokes it with -c "<command>". noshell looks up the command in a YAML
allowlist and either runs it (logging to syslog) or denies it.

Invoked with no command, noshell prints the allowed commands and exits.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          run,
}

// Execute is the entrypoint used by the root main.go.
func Execute(version, commit, date string) {
	rootCmd.Version = fmt.Sprintf("%s (commit %s, built %s)", version, commit, date)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "noshell: %v\n", err)
		os.Exit(exitGeneric)
	}
}

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "config file (default "+defaultConfigPath+", overridden by $"+envConfigPath+")")
	rootCmd.PersistentFlags().StringVarP(&commandFlag, "command", "c", "", "command to run (shell mode, used by sshd)")
}

// initConfig resolves the config file path with the precedence
// --config flag > $NOSHELL_CONFIG > /etc/noshell/noshell.yml
// and primes viper with it. Viper isn't unmarshaling the polymorphic
// `commands` schema (config.Parse handles that), but having the path
// recorded via viper makes future env-var/flag bindings trivial.
func initConfig() {
	if configFile == "" {
		configFile = os.Getenv(envConfigPath)
	}
	if configFile == "" {
		configFile = defaultConfigPath
	}
	viper.SetConfigFile(configFile)
	viper.SetEnvPrefix("noshell")
	viper.AutomaticEnv()
}

func run(_ *cobra.Command, args []string) error {
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
