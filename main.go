package main

import "github.com/binwiederhier/rrsh/cmd"

// Populated by -ldflags at build time.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	cmd.Execute(version, commit, date)
}
