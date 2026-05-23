package config

import "regexp"

// validUsername: POSIX login name (lowercase start, alnum/underscore/dash).
var validUsername = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// rawConfig/rawRule are the JSON-decode targets. Parse converts them
// into Config/CommandRule after validation and regex compilation.

type rawConfig struct {
	Instructions string    `json:"instructions"`
	Commands     []rawRule `json:"commands"`
}

type rawRule struct {
	Command     []string `json:"command"`
	Timeout     string   `json:"timeout"`
	As          []string `json:"as"`
	Description string   `json:"description"`
}
