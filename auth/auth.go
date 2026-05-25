package auth

// SelfUser is the magic token in a rule's "as:" list meaning "the SSH
// user who invoked rrsh". Spelled "$USER" so it can't collide with a
// real POSIX username (which can't start with "$"). The matcher
// substitutes it with its currentUser at Match time.
const SelfUser = "$USER"
