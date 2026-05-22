package config

import "regexp"

// SelfUser is the magic token in `as:` lists meaning "the SSH user who
// invoked rrsh". Resolved at runtime against $SUDO_USER (privileged
// subcommand) or the current user (unprivileged process).
const SelfUser = "self"

// validUsername is a conservative POSIX login-name pattern: lowercase
// letter or underscore start, then alnum/underscore/dash.
var validUsername = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// rawConfig and rawRule are the on-the-wire shapes that the JSON
// decoder targets. Parse converts them into the public Config /
// CommandRule structs after validation and regex compilation.

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
