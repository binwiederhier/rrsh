// Package cmd is rrsh's CLI entry point and subcommand dispatcher.
//
// The dispatch is intentionally hand-rolled (no cobra) so the binary has no
// external runtime dependencies — important for a security-critical tool
// where every dependency is part of the trust boundary.
package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/binwiederhier/rrsh/mcp"
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
	mcp.Version = version

	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "sudo":
			sudoMain(args[1:])
			return
		case "-h", "-help", "--help":
			printUsage(os.Stdout)
			return
		case "-v", "-version", "--version":
			fmt.Println(versionInfo)
			return
		}
	}
	serveMain(args)
}

const usageText = `rrsh — JSON-RPC server for AI-driven remote command execution

Usage:
  rrsh                              read JSON-RPC requests from stdin (default)
  rrsh --config <file>              same, with explicit config path
  rrsh --help | --version

Claude integration:
  claude mcp add rrsh-prod -- ssh -T <user>@<host>

Options:
  --config <file>   config file (default: ` + defaultConfigPath + `,
                    overridden by $` + envConfigPath + `)
  --help            print this help
  --version         print version

Internal:
  rrsh sudo <path> <argv...>   privileged half (called via /usr/bin/sudo).
                               Re-validates against the allowlist.
`

func printUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}
