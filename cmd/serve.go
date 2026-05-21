package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/logger"
	"github.com/binwiederhier/rrsh/server"
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
	} else if *showHelp {
		printUsage(os.Stdout)
		return
	} else if *showVersion {
		printVersion(os.Stdout)
		return
	} else if *commandFlag != "" || fs.NArg() > 0 || isTerminal(os.Stdin) {
		printShellHelp(os.Stderr)
		os.Exit(exitGeneric)
	}

	// Figure out user
	u, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: cannot determine current user: %v\n", err)
		os.Exit(exitGeneric)
	}

	// Start syslog logger
	log := logger.New(u.Username)
	defer log.Close()

	// Load config
	conf, err := config.Load(resolveConfigPath(*configFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}

	// Run JSON-RPC server
	srv, err := server.New(conf, log, u.Username, os.Stdin, os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}
	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: %v\n", err)
		os.Exit(exitGeneric)
	}
}

func printShellHelp(w io.Writer) {
	target := sshTargetHint()
	fmt.Fprintf(w, `rrsh: a JSON-RPC server for server diagnostics, not an interactive shell.

Send newline-delimited JSON-RPC 2.0 requests over SSH stdin. Two methods:
  hello  - name, host-specific instructions, and the full allowlist
  run    - execute one allowlisted command, or a pipeline of stages

Every response is wrapped in {"jsonrpc":"2.0","id":<your-id>, ...}. Errors
(allowlist denial, elevation off, oversize input) come back as
{"error":{"code":-32000,"message":"..."}}. A child's non-zero exit is NOT
an error - it lives in result.exit alongside stdout, stderr, timed_out,
and truncated. Always send a unique numeric "id" so you can correlate.

1) Discover what this host permits. Call this ONCE up front (or whenever
   you've forgotten the allowlist) - it is NOT required before every run,
   just for initial discovery:

  echo '{"jsonrpc":"2.0","id":1,"method":"hello"}' | ssh -T %[1]s

   The result has {name, instructions, commands}. Read
   "instructions" - it's host-specific guidance. Each entry in
   "commands" has a regex list: command[0] matches argv[0] (the path),
   command[i] matches argv[i]. len(argv) must equal len(command).
   Once you know what's allowed, jump straight to "run" - no need to
   resend "hello" for subsequent calls.

2) Run one command as the SSH user (default):

  echo '{"jsonrpc":"2.0","id":2,"method":"run",
         "params":{"argv":["/usr/bin/whoami"]}}' | ssh -T %[1]s

   Result: {"stdout":"...","stderr":"...","exit":0}

3) Run as root. Requires the matched rule's "as" list to include "root"
   AND the host config to have "sudo":true. If the rule's "as" list has
   exactly one non-self user, you can OMIT "as" and rrsh auto-elevates
   (the common "always root" case):

  echo '{"jsonrpc":"2.0","id":3,"method":"run","params":{
         "argv":["/usr/bin/journalctl","-u","ntfy","-n","100"],
         "as":"root"}}' | ssh -T %[1]s

4) Pipe data INTO a command (no shell involved - this is just a string
   handed to the child's stdin):

  echo '{"jsonrpc":"2.0","id":4,"method":"run","params":{
         "argv":["/usr/bin/grep","-i","error"],
         "stdin":"foo\nERROR: bar\nbaz\n"}}' | ssh -T %[1]s

5) Pipeline - chain stages with the "pipeline" array. There is NO shell
   anywhere in rrsh; "|" and ">" inside an argv element are literal
   bytes, not metacharacters. Each stage is independently matched and
   authorized. stdout of stage i feeds stdin of stage i+1. Per-stage
   "as" lets an elevated stage feed an unprivileged filter:

  echo '{"jsonrpc":"2.0","id":5,"method":"run","params":{"pipeline":[
         {"argv":["/usr/bin/journalctl","-u","ntfy","-n","1000"],"as":"root"},
         {"argv":["/usr/bin/grep","-i","error"]}
       ]}}' | ssh -T %[1]s

6) Batch multiple requests in one SSH session, one JSON object per line:

  printf '%%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"hello"}' \
    '{"jsonrpc":"2.0","id":2,"method":"run","params":{"argv":["/usr/bin/whoami"]}}' \
    '{"jsonrpc":"2.0","id":3,"method":"run","params":{"argv":["/usr/bin/uptime"]}}' \
    | ssh -T %[1]s

Constraints:
  - argv[0] must be an absolute path (start with "/").
  - Per-command timeout is 30s unless the rule says otherwise; timeouts
    return exit=124, timed_out=true.
  - Stdout and stderr are each capped at 10 MiB; overflow is dropped and
    truncated=true.
  - Per-request line is capped at 1 MiB. Pipelines are capped at 16 stages.
  - Use "ssh -T" (no PTY). Without it, ssh allocates a TTY and rrsh kicks
    you back to this message instead of speaking JSON-RPC.
`, target)
}

// sshTargetHint returns user@host for help text. Failures fall back to
// generic placeholders - we're already exiting with an error and the
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
// character device). Uses the file's stat mode - no CGO, no external
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
