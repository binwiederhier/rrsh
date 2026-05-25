package cmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"

	"github.com/binwiederhier/rrsh/audit"
	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/server"
)

// runServe is the default code path: a JSON-RPC server over stdin/stdout.
// `-c <cmd>`, positional args, or an interactive TTY all error out
// pointing the caller to the JSON-RPC protocol.
func runServe(args []string) {
	fs := flag.NewFlagSet("rrsh", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printUsage(os.Stderr)
	}
	var (
		commandFlag = fs.String("c", "", "")
		showHelp    = fs.Bool("help", false, "")
		showVersion = fs.Bool("version", false, "")
	)
	fs.Parse(args) // ExitOnError handles parse failures
	if *showHelp {
		printUsage(os.Stdout)
		return
	}
	if *showVersion {
		printVersion(os.Stdout)
		return
	}
	if *commandFlag != "" || fs.NArg() > 0 || isTerminal(os.Stdin) {
		printShellHelp(os.Stderr)
		os.Exit(exitGeneric)
	}

	// Resolve the SSH user
	u, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rrsh: cannot determine current user: %v\n", err)
		os.Exit(exitGeneric)
	}

	// Start syslog audit logger
	log := audit.New()
	defer log.Close()

	// Load config
	conf, err := config.Load(configPath)
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

Send newline-delimited JSON-RPC 2.0 requests over SSH stdin. Three methods:
  list_commands  - host-specific instructions and the full allowlist
  run_command    - execute one allowlisted command
  run_pipeline   - chain stages with native Go pipes (no shell)

Every response is wrapped in {"jsonrpc":"2.0","id":<your-id>, ...}. Errors
(allowlist denial, elevation off, oversize input) come back as
{"error":{"code":-32000,"message":"..."}}. A child's non-zero exit is NOT
an error - it lives in result.exit alongside stdout, stderr, timed_out,
and truncated. Always send a unique numeric "id" so you can correlate.

1) Discover what this host permits. Call this ONCE up front (or whenever
   you've forgotten the allowlist) - it is NOT required before every run,
   just for initial discovery:

   $ echo '{"jsonrpc":"2.0","id":1,"method":"list_commands"}' | ssh -T %[1]s

   The result has {instructions, commands}. Read
   "instructions" - it's host-specific guidance. Each entry in
   "commands" has its own "command" field - a regex list where element
   0 matches the path you send and element i matches your i-th argument.
   The request's "command" array length must equal the rule's "command"
   length. Once you know what's allowed, jump straight to "run_command"
   or "run_pipeline" - no need to resend "list_commands" for subsequent calls.

2) Run one command as the SSH user (default):

   $ echo '{"jsonrpc":"2.0","id":2,"method":"run_command",
         "params":{"command":["/usr/bin/whoami"]}}' | ssh -T %[1]s

   Result: {"stdout":"...","stderr":"...","exit":0}

3) To run a command as root (or another user), pass the "as" parameter, e.g. "as": "root".

   $ echo '{"jsonrpc":"2.0","id":3,"method":"run_command","params":{
         "command":["/usr/bin/journalctl","-u","ntfy","-n","100"],
         "as":"root"}}' | ssh -T %[1]s

4) Pipe data INTO a command (no shell involved - this is just a string
   handed to the child's stdin):

   $ echo '{"jsonrpc":"2.0","id":4,"method":"run_command","params":{
         "command":["/usr/bin/grep","-i","error"],
         "stdin":"foo\nERROR: bar\nbaz\n"}}' | ssh -T %[1]s

5) Pipeline - chain stages with run_pipeline. There is NO shell
   anywhere in rrsh; "|" and ">" inside a command element are literal
   bytes, not metacharacters. Each stage is independently matched and
   authorized. stdout of stage i feeds stdin of stage i+1. Per-stage
   "as" lets an elevated stage feed an unprivileged filter:

   $ echo '{"jsonrpc":"2.0","id":5,"method":"run_pipeline","params":{"pipeline":[
         {"command":["/usr/bin/journalctl","-u","ntfy","-n","1000"],"as":"root"},
         {"command":["/usr/bin/grep","-i","error"]}
       ]}}' | ssh -T %[1]s

6) Batch multiple requests in one SSH session, one JSON object per line:

   $ printf '%%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"list_commands"}' \
    '{"jsonrpc":"2.0","id":2,"method":"run_command","params":{"command":["/usr/bin/whoami"]}}' \
    '{"jsonrpc":"2.0","id":3,"method":"run_command","params":{"command":["/usr/bin/uptime"]}}' \
    | ssh -T %[1]s

Constraints:
  - command[0] must be an absolute path (start with "/").
  - Per-command timeout is 30s unless the rule says otherwise; timeouts
    return exit=124, timed_out=true.
  - Stdout and stderr are each capped at 10 MiB; overflow is dropped and
    truncated=true.
  - Per-request line is capped at 1 MiB. Pipelines are capped at 16 stages.
  - Use "ssh -T" (no PTY). Without it, ssh allocates a TTY and rrsh kicks
    you back to this message instead of speaking JSON-RPC.
`, target)
}

// sshTargetHint returns user@host for help text, with placeholder
// fallbacks if either lookup fails.
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

// isTerminal reports whether f is a character device. False on Stat
// error. Stat-based check avoids CGO.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
