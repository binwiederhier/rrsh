package cmd

import (
	"flag"
	"fmt"
	"os"
	"os/user"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/logger"
	"github.com/binwiederhier/rrsh/mcp"
)

// runServe is the default code path: a JSON-RPC server over stdin/stdout.
// `-c <cmd>`, positional args, or an interactive TTY all error out
// pointing the caller to the JSON-RPC protocol.
func runServe(args []string) {
	fs := flag.NewFlagSet("rrsh", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printUsage(os.Stderr)
	}
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

	if *commandFlag != "" || fs.NArg() > 0 || isTerminal(os.Stdin) {
		printShellModeRejection(os.Stderr)
		os.Exit(exitGeneric)
	}

	// Every downstream security decision needs the current user; fail
	// closed rather than guess if the lookup fails.
	u, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: cannot determine current user: %v\n", err)
		os.Exit(exitGeneric)
	}

	log := logger.New(u.Username)
	defer log.Close()

	cfg, err := config.Load(resolveConfigPath(*configFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	rrshBin, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: cannot resolve own executable path: %v\n", err)
		os.Exit(exitGeneric)
	}

	srv := mcp.New(cfg, log, u.Username, rrshBin, os.Stdin, os.Stdout)
	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}
}

// printShellModeRejection tells the caller (likely an AI trying
// `ssh user@host whoami`) that rrsh is a JSON-RPC server and shows
// enough to recover. Long-form on purpose: this is the breadcrumb that
// turns a wrong first attempt into a working one.
func printShellModeRejection(w *os.File) {
	target := sshTargetHint()
	fmt.Fprintf(w, `rrsh: this is a JSON-RPC server, not an interactive shell.

Send newline-delimited JSON-RPC 2.0 requests over SSH stdin. Tools are:
  - list_commands — describes what this host permits
  - run_command   — runs one command or a pipeline

A typical first session looks like:

  printf '%%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ai","version":"0"}}}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_commands","arguments":{}}}' \
    | ssh -T %s

The initialize response carries an "instructions" field with host-specific
guidance — read it first.
`, target)
}

// sshTargetHint returns user@host for help text. Failures fall back to
// generic placeholders — we're already exiting with an error and the
// example only needs to be copy-pasteable.
func sshTargetHint() string {
	username := "<user>"
	if u, err := user.Current(); err == nil && u.Username != "" {
		username = u.Username
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "<host>"
	}
	return username + "@" + host
}

// isTerminal returns true if f is connected to a terminal (i.e. a
// character device). Uses the file's stat mode — no CGO, no external
// dependency. False if Stat fails.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
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
