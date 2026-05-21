package cmd

import (
	"fmt"
	"io"
	"os"
)

const (
	defaultConfigPath = "/etc/rrsh/rrsh.json"
	envConfigPath     = "RRSH_CONFIG"

	exitDenied  = 126
	exitGeneric = 1
)

// versionInfo is populated by Execute from main.go's ldflag-injected vars.
var versionInfo string

// Execute is the entrypoint called from main.go.
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
  rrsh                              read JSON-RPC requests from stdin (default)
  rrsh --config <file>              same, with explicit config path
  rrsh --help | --version

AI integration:
  Mention "ssh -T <user>@<host>" in CLAUDE.md/AGENTS.md so the AI calls hello
  (returns the full allowlist) and then run_command or run_pipeline
  directly over SSH stdin.

Options:
  --config <file>   config file (default: `+defaultConfigPath+`,
                    overridden by $`+envConfigPath+`)
  --help            print this help
  --version         print version
`)
}
