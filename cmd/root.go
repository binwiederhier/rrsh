package cmd

import (
	"fmt"
	"io"
	"os"
)

const (
	configPath = "/etc/rrsh/rrsh.json"

	exitDenied  = 126
	exitGeneric = 1
)

var versionInfo string

// Execute is the CLI entrypoint.
func Execute(version, commit, date string) {
	versionInfo = fmt.Sprintf("rrsh %s (commit %s, built %s)", version, commit, date)

	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "sudo":
			runSudo(args[1:])
			return
		case "-h", "-help", "--help":
			printUsage(os.Stdout)
			return
		case "-v", "-version", "--version":
			printVersion(os.Stdout)
			return
		}
	}
	runServe(args)
}

func printVersion(w io.Writer) {
	fmt.Fprintln(w, versionInfo)
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `rrsh - JSON-RPC server for AI-driven remote command execution

Usage:
  rrsh                       read JSON-RPC requests from stdin (default)
  rrsh --help | --version    print help/version

The config path is fixed at `+configPath+` - both the unprivileged
server side and the privileged "rrsh sudo" subcommand read it.

AI integration:
  Mention "ssh -T <user>@<host>" in CLAUDE.md/AGENTS.md so the AI calls list_commands
  (returns the full allowlist) and then run_command or run_pipeline
  directly over SSH stdin.

Options:
  --help            print this help
  --version         print version
`)
}
