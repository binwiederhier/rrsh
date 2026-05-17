// Package cmd is rrsh's CLI entry point and subcommand dispatcher.
//
// The dispatch is intentionally hand-rolled (no cobra) so the binary has no
// external runtime dependencies — important for a security-critical tool where
// every dependency is part of the trust boundary.
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

	sudoBinary = "/usr/bin/sudo"
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
			sudoMain(args[1:])
			return
		case "run":
			runMain(args[1:])
			return
		case "-h", "-help", "--help":
			printUsage(os.Stdout)
			return
		case "-v", "-version", "--version":
			fmt.Println(versionInfo)
			return
		}
	}
	runMain(args)
}

const usageText = `rrsh — restricted login shell that allowlists commands

Usage:
  rrsh                                 print the allowlist
  rrsh -c <command>                    run <command> (shell mode, used by sshd)
  rrsh [--config FILE] [--] <command> [args...]
                                          run <command> directly
  rrsh run [...]                       alias for the default behavior
  rrsh --help | --version

Options:
  -c <command>      command to run (shell mode)
  --config <file>   config file (default: ` + defaultConfigPath + `,
                    overridden by $` + envConfigPath + `)
  --help            print this help
  --version         print version

Commands may be prefixed with "sudo" or "sudo -u USER" to request elevation;
the rule's "as" list controls which target users are permitted.
`

func printUsage(w io.Writer) {
	fmt.Fprint(w, usageText)
}
