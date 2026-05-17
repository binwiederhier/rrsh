package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/executor"
	"github.com/binwiederhier/rrsh/logger"
	"github.com/binwiederhier/rrsh/matcher"
)

// runMain is the default code path (also reached via `rrsh run …`).
// It accepts the canonical sshd invocation `rrsh -c "<cmd>"` as well as
// `rrsh [--config FILE] [--] <cmd> [args...]` for direct use.
func runMain(args []string) {
	fs := flag.NewFlagSet("rrsh", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printUsage(os.Stderr) }
	var (
		configFile  = fs.String("config", "", "")
		commandFlag = fs.String("c", "", "")
		showHelp    = fs.Bool("help", false, "")
		showVersion = fs.Bool("version", false, "")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(exitGeneric)
	}
	if *showHelp {
		printUsage(os.Stdout)
		return
	}
	if *showVersion {
		fmt.Println(versionInfo)
		return
	}

	log := logger.New()
	defer log.Close()

	cfg, err := config.Load(resolveConfigPath(*configFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	input := resolveInput(*commandFlag, fs.Args())
	if input == "" {
		printAllowed(cfg)
		return
	}

	requested, bare := parseSudoPrefix(input)
	if bare == "" {
		fmt.Fprintf(os.Stderr, "rrsh: missing command after sudo\n")
		os.Exit(exitDenied)
	}

	self := currentUsername()

	rule, ok := matcher.New(cfg.Commands).Match(bare)
	if !ok {
		log.Denied(input, self)
		fmt.Fprintf(os.Stderr, "rrsh: command not allowed: %s\n", input)
		os.Exit(exitDenied)
	}

	target := resolveTarget(requested, rule.As, self)
	if target == "" {
		log.Denied(input, self)
		fmt.Fprintf(os.Stderr, "rrsh: %s not permitted to run as %s\n", bare, displayTarget(requested, self))
		os.Exit(exitDenied)
	}

	if target == self {
		log.Allowed(input, self)
		os.Exit(executor.New(cfg.Timeout).Execute(bare, rule))
	}

	log.Allowed(input, target)
	os.Exit(elevate(target, bare, rule, cfg.Timeout))
}

// resolveConfigPath applies the precedence --config > $RRSH_CONFIG > default.
func resolveConfigPath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if env := os.Getenv(envConfigPath); env != "" {
		return env
	}
	return defaultConfigPath
}

func resolveInput(commandFlag string, args []string) string {
	if commandFlag != "" {
		return commandFlag
	}
	if len(args) > 0 {
		return strings.Join(args, " ")
	}
	return ""
}

// elevate re-invokes the rrsh binary as `target` via /usr/bin/sudo. The
// privileged process (cmd/sudo.go) re-validates the command from disk before
// executing — this side cannot be trusted by it.
func elevate(target, bare string, rule *config.CommandRule, globalTimeout time.Duration) int {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
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
