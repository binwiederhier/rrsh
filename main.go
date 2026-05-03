package main

import (
	"fmt"
	"os"
	"strings"
)

const defaultConfigPath = "/etc/noshell/noshell.yml"

func main() {
	initLogger()

	configPath, input := parseArgs(os.Args[1:])

	if configPath == "" {
		configPath = os.Getenv("NOSHELL_CONFIG")
	}
	if configPath == "" {
		configPath = defaultConfigPath
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "noshell: %v\n", err)
		os.Exit(1)
	}

	if input == "" {
		printAllowedCommands(cfg)
		os.Exit(0)
	}

	matched, rule := Match(cfg.Commands, input)
	if !matched {
		logDenied(input)
		fmt.Fprintf(os.Stderr, "noshell: command not allowed: %s\n", input)
		os.Exit(126)
	}

	logAllowed(input)
	exitCode := Execute(input, rule, cfg.Timeout)
	os.Exit(exitCode)
}

// printAllowedCommands prints all allowed commands and their argument patterns.
func printAllowedCommands(cfg *Config) {
	fmt.Println("Allowed commands:")
	for _, rule := range cfg.Commands {
		if rule.ArgsPattern != nil {
			fmt.Printf("  %s %s\n", rule.Path, rule.ArgsPattern.String())
		} else {
			fmt.Printf("  %s\n", rule.Path)
		}
	}
}

// parseArgs parses noshell's own flags and extracts the command to run.
// It supports two modes:
//   - Shell mode: noshell -c "command args"
//   - Direct mode: noshell [--config=FILE] [--] command [args...]
//
// Returns the config path (empty string if not specified) and the command input string.
func parseArgs(args []string) (string, string) {
	configPath := ""

	// Shell mode: look for -c flag first (used when noshell is a login shell)
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" && i+1 < len(args) {
			return configPath, args[i+1]
		}
		if strings.HasPrefix(args[i], "--config=") {
			configPath = strings.TrimPrefix(args[i], "--config=")
		}
	}

	// Direct mode: parse noshell flags, then treat the rest as the command
	i := 0
	for i < len(args) {
		if args[i] == "--" {
			i++
			break
		}
		if strings.HasPrefix(args[i], "--config=") {
			configPath = strings.TrimPrefix(args[i], "--config=")
			i++
			continue
		}
		// First non-flag argument starts the command
		break
	}

	if i >= len(args) {
		return configPath, ""
	}

	return configPath, strings.Join(args[i:], " ")
}
