# rrsh — really restricted shell

A JSON-RPC server that lets an AI agent (Claude, mostly) run a curated set of commands on a remote host. Installs as a user's login shell so sshd handles auth and transport — no daemon to keep running, no port to firewall, no auth code in rrsh itself.

Useful for letting Claude SSH into a server and do bounded diagnostic work (`systemctl status`, `journalctl -u …`, `tail /var/log/…`, etc.) without giving it a real shell. Individual commands can additionally be marked runnable as `root` (or any other user) via a single sudoers line — see [Elevation](#elevation).

Zero runtime dependencies (Go stdlib only). Single static binary.

## How it works

- Installed as a user's login shell. sshd authenticates the SSH client and execs `/usr/bin/rrsh` with the connection's stdio.
- rrsh reads newline-delimited JSON-RPC 2.0 requests on stdin and writes responses on stdout. No shell-string parsing, no `-c` mode.
- Two MCP tools are exposed: `list_commands` (describes what is allowed) and `run_command` (executes one command or a pipeline of them).
- Arguments are passed as a real `argv` array — quoting, embedded spaces, and literal metacharacters in argument *values* are not a parser concern.
- Commands are matched against `/etc/rrsh/rrsh.json` rules (override with `--config=` or `$RRSH_CONFIG`). Each rule is an absolute binary path, optionally with a regex the joined argument string must match, a per-command timeout, and a list of users the command may run as.
- Allow/deny decisions go to syslog (`auth.info` / `auth.warning`).

## Install

Download a `.deb` or `.rpm` from the releases page, then:

```bash
sudo dpkg -i rrsh_*.deb
# or
sudo rpm -i rrsh-*.rpm
```

This installs the binary to `/usr/bin/rrsh` and an example config to `/etc/rrsh/rrsh.json`.

## Set up the SSH user

1. Edit `/etc/rrsh/rrsh.json` with the commands you want to allow (see below).
2. Register the shell:
   ```bash
   echo /usr/bin/rrsh | sudo tee -a /etc/shells
   ```
3. Create the SSH user with rrsh as its shell:
   ```bash
   sudo useradd -m -s /usr/bin/rrsh ai
   sudo mkdir -p ~ai/.ssh
   sudo cp ~/.ssh/id_ed25519.pub ~ai/.ssh/authorized_keys
   sudo chown -R ai:ai ~ai/.ssh && sudo chmod 700 ~ai/.ssh && sudo chmod 600 ~ai/.ssh/authorized_keys
   ```

## Connect from Claude

Register rrsh as an MCP server. The transport is `ssh` itself — `ssh -T` opens a stdio pipe to the remote rrsh process, which is exactly what MCP needs.

```bash
claude mcp add rrsh-prod -- ssh -T ai@prod.example.com
```

That's it. No daemon on the server, no port forward, no Subsystem config in sshd. The same `chsh` setup that gates the SSH session gates the MCP channel.

In a Claude session, the two tools (`list_commands`, `run_command`) become available. Claude is expected to call `list_commands` first to discover what is permitted, then construct `run_command` calls with a structured `argv` slice.

## Configuration

`/etc/rrsh/rrsh.json`:

```json
{
  "timeout": "10s",
  "commands": [
    { "path": "/usr/bin/whoami",
      "description": "Show the effective username." },

    { "path": "/usr/bin/journalctl", "args": "^-u .+$",
      "description": "Show the journal for a systemd unit." },

    { "path": "/usr/bin/ping",       "args": "^-c \\d+ .+$", "timeout": "30s",
      "description": "Ping a host a fixed number of times." },

    { "path": "/bin/systemctl",      "args": "^restart ntfy$",  "as": ["root"],
      "description": "Restart the ntfy systemd unit." },

    { "path": "/bin/journalctl",     "args": "^-fu ntfy$",      "as": ["self", "root"],
      "description": "Follow the ntfy unit log." }
  ]
}
```

Fields on each command entry:

| Field         | Default          | Meaning                                                                          |
| ------------- | ---------------- | -------------------------------------------------------------------------------- |
| `path`        | required         | Absolute path to the binary.                                                     |
| `args`        | any args allowed | Regex the joined argument string must match (anchored as written).               |
| `timeout`     | global timeout   | Per-command timeout, e.g. `"30s"`. Overrides the top-level `timeout`.            |
| `as`          | `["self"]`       | Users the command may run as. `self` resolves to the SSH user at runtime.        |
| `description` | empty            | Free-text shown to Claude via `list_commands`. Treat it like an API doc string. |

Rules:

- Paths must be absolute. Relative paths are rejected.
- The args regex matches the space-joined argv slice (so a rule like `^-u .+$` allows `["-u", "ntfy"]`).
- Rules are matched in order; the first rule with a matching `path` decides the outcome. If its args regex doesn't match, the call is denied — later rules with the same path are not tried.
- Unknown JSON fields are rejected — typos in the config fail fast rather than silently weakening the policy.

## The two MCP tools

### `list_commands`

No arguments. Returns the rule set as a JSON document inside the MCP text content. Each entry has `path`, `args_pattern` (omitted when none), `as`, `description`, and `timeout_seconds` (omitted when none). Claude calls this to learn what is allowed.

### `run_command`

Executes one allowlisted command, or a pipeline of them. Exactly one of `argv` or `pipeline` must be set.

**Single command:**

```json
{
  "name": "run_command",
  "arguments": {
    "argv": ["/usr/bin/journalctl", "-u", "ntfy", "-n", "100"],
    "as": "root",
    "stdin": "optional input"
  }
}
```

**Pipeline:** stdout of stage *i* is wired to stdin of stage *i+1* via native Go pipes — no shell is invoked anywhere.

```json
{
  "name": "run_command",
  "arguments": {
    "pipeline": [
      { "argv": ["/usr/bin/journalctl", "-u", "ntfy", "-n", "1000"], "as": "root" },
      { "argv": ["/usr/bin/grep", "ERROR"] }
    ]
  }
}
```

Each stage is independently matched against the allowlist and authorized for its `as` user. Per-stage `as` lets an elevated stage feed an unprivileged filter.

**Return value:** structured JSON inside the MCP text content:

```json
{ "stdout": "...", "stderr": "...", "exit": 0, "timed_out": false, "truncated": false }
```

- Stdout and stderr are captured separately. They are returned as UTF-8 strings, with invalid bytes replaced by U+FFFD.
- Each stream is capped at 10 MB; further bytes are dropped and `truncated: true` is set.
- Exit code is the child's exit (or last stage's exit for a pipeline). Timeouts return exit `124` with `timed_out: true`.
- `isError: true` is set on the MCP envelope when the call was denied **or** when the exit code is non-zero, so Claude can short-circuit on either.

## Elevation

When a rule's `as` list contains a user other than `self`, Claude can request that target by passing `"as": "<user>"` on the `run_command` call (or per-stage in a pipeline):

| Tool call                                                  | Resolves to                                       |
| ---------------------------------------------------------- | ------------------------------------------------- |
| `run_command({argv: [...]})`                               | run as the SSH user (default)                     |
| `run_command({argv: [...], as: "root"})`                   | run as `root` (if `root` is in the rule's `as`)   |
| `run_command({argv: [...], as: "deploy"})`                 | run as `deploy`                                   |
| `run_command({argv: ["/bin/systemctl","restart","ntfy"]})` | implicit `root` for single-target rules           |

For rules whose `as` list contains exactly one non-self target, Claude does not need to pass `as` — rrsh implicitly elevates. This is the common "always root" case.

Internally, rrsh re-execs itself via `/usr/bin/sudo` to perform the privilege transition. That's the only invocation of real sudo and it is gated by one sudoers line:

```
# /etc/sudoers.d/ai
ai ALL=(root,deploy) NOPASSWD: /usr/bin/rrsh sudo *
```

List every target user that appears in any rule's `as:` list in the `(...)` part. The privileged half of rrsh (`rrsh sudo <path> <argv...>`, hidden subcommand) re-reads `/etc/rrsh/rrsh.json` from disk and re-validates the command against the rule's `as` list before executing — it never trusts its caller, does no flag parsing, and takes the originating user from `$SUDO_USER`.

**Trust boundary:** a parser/match bug in rrsh is a root compromise. Keep `as:` lists minimal, keep `args` regexes tight, and prefer one narrow rule per elevated command over a single permissive rule.

## Logging

Decisions go to syslog under the `rrsh` tag, facility `auth`:

```
Mar  5 21:22:01 host rrsh[12345]: ALLOWED: user=ai cmd=/usr/bin/whoami
Mar  5 21:22:14 host rrsh[12346]: DENIED: user=ai cmd=/bin/sh
Mar  5 21:22:30 host rrsh[12347]: ALLOWED: user=ai as=root cmd=/bin/systemctl restart ntfy
Mar  5 21:22:45 host rrsh[12348]: DENIED: user=ai as=root cmd=/usr/bin/whoami
Mar  5 21:22:55 host rrsh[12349]: ALLOWED: user=ai cmd=/usr/bin/journalctl -u ntfy -n 1000 | /usr/bin/grep ERROR
```

The `as=` field is present only when the executing user differs from the SSH user. Pipelines are logged as the space-joined stages separated by ` | `. On Debian/Ubuntu these typically end up in `/var/log/auth.log`; on RHEL-likes in `/var/log/secure`.

## Why JSON-RPC, not shell-string parsing

SSH joins the client's arguments into a single string before sshd ever sees them, so a restricted shell that reads `-c "..."` is fundamentally a string parser. Quoting, embedded spaces, and metacharacters become parser concerns — and any parser bug is on the privileged trust boundary.

rrsh used to be a string parser. It isn't any more. With JSON-RPC, every argument arrives in its own array slot, so input like `grep " | > /dev/null"` is unambiguous: the literal pipe-and-redirect string is one element in `argv`, not a shell metacharacter. The matcher is a regex over the joined form; the executor invokes the binary with the original slice. No tokenization layer to write or audit.

If you need a human-friendly CLI on top of this, write one in your client (it can call `run_command` directly). rrsh's job is to be a safe, structured endpoint.

## Build from source

```bash
go build -o rrsh .
```

Requires Go 1.25+. No CGO, no external dependencies — `go.mod` lists only the module itself.

## License

Apache 2.0
