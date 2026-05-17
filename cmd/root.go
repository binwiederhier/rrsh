package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	defaultConfigPath = "/etc/noshell/noshell.yml"
	envConfigPath     = "NOSHELL_CONFIG"
)

var (
	configFile  string
	commandFlag string
)

var rootCmd = &cobra.Command{
	Use:   "noshell",
	Short: "Restricted login shell that allowlists commands via YAML",
	Long: `noshell is a restricted login shell. When set as a user's login shell,
sshd invokes it with -c "<command>". noshell looks up the command in a YAML
allowlist and either runs it (logging to syslog) or denies it.

Invoked with no command, noshell prints the allowed commands and exits.
The "run" subcommand exposes the same behavior explicitly; sshd-style
"noshell -c <command>" invocations at the root delegate to it.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runE,
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
	rootCmd.Flags().StringVarP(&commandFlag, "command", "c", "", "command to run (shell mode, used by sshd)")
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
