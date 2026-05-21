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

// runServe is the default code path. rrsh exposes a JSON-RPC server over
// stdin/stdout — no shell-string parsing, no `-c` mode. When invoked with
// `-c "..."` or positional args, it errors out pointing the caller to MCP.
//
// Typical invocations:
//
//	rrsh                         # sshd login shell: read NDJSON from stdin
//	rrsh --config /etc/rrsh.json # same, custom config
//
// Anything else is the legacy shell mode that has been removed.
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

	// Resolve the current user up front: every downstream security
	// decision (self vs target user, syslog tagging) needs it, and there
	// is no useful fallback — if the kernel can't tell us who we are,
	// fail closed rather than guess.
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

// printShellModeRejection is the breadcrumb shown when something (a human
// or an AI agent) tries to use rrsh as a traditional shell. It is the AI's
// most likely first encounter with rrsh: a one-line instruction like
// "you can diagnose the host via ssh rrsh@server" will most often result
// in the AI trying `ssh rrsh@server whoami` or similar first. The text
// here has to be enough for the AI to recover and discover JSON-RPC on
// its own.
func printShellModeRejection(w *os.File) {
	fmt.Fprint(w, `rrsh: this is a JSON-RPC server, not an interactive shell.

To use it, send newline-delimited JSON-RPC 2.0 requests over SSH stdin:

  echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
    | ssh -T `+sshTargetHint()+`

Two tools are exposed:
  - list_commands  — describes which commands this host permits
  - run_command    — runs one command (argv slice) or a pipeline

Typical first session:

  printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"ai","version":"0"}}}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_commands","arguments":{}}}' \
    | ssh -T `+sshTargetHint()+`

The initialize response includes an "instructions" field with host-specific
guidance — read it first.
`)
}

// sshTargetHint returns user@host derived from the environment, falling
// back to generic placeholders. We deliberately tolerate user.Current
// failures here — we're already printing an error and exiting; a missing
// username just means the example uses "<user>" instead of being
// copy-pasteable. The fatal-on-error policy applies to the live JSON-RPC
// path, not the help text.
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
