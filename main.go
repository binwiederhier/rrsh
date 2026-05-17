package main

import "github.com/pheckel/noshell/cmd"

// These variables are set during build time using -ldflags
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	cmd.Execute(version, commit, date)
}
