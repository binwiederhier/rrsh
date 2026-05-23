# rrsh - really restricted shell

A JSON-RPC server that lets an AI agent (Claude, Cursor) run a curated set of commands on a remote host. Installs as a user's login shell so sshd handles auth and transport - no daemon to keep running, no port to firewall, no auth code in rrsh itself. The project has zero runtime dependencies (Go stdlib only).

Useful for letting your AI log into a server and do bounded diagnostic work (`systemctl status`, `journalctl -u ...`, `tail /var/log/...`, etc.) without giving it a real shell.

Here's a super simple example:

```bash
# Lists all allowed commands
echo '{"jsonrpc":"2.0","id":1,"method":"list_commands"}' | ssh -T -i ~/.ssh/id_ai rrsh@host 

# Do some diagnostics (depends on allowed commands)
echo '{"jsonrpc":"2.0","id":2,"method":"run_command","params":{"argv":["/usr/bin/systemctl", "status", "ntfy"]}}' | ssh -T -i ~/.ssh/id_ai rrsh@host
echo '{"jsonrpc":"2.0","id":2,"method":"run_command","params":{"argv":["/usr/bin/last", "-n", "5"]}}' | ssh -T -i ~/.ssh/id_ai rrsh@host  
```

## Installation

1. **Install the package.** Download a `.deb` or `.rpm` from the releases page:

   ```bash
   sudo dpkg -i rrsh_*.deb
   # or
   sudo rpm -i rrsh-*.rpm
   ```

2. **Write the config** at `/etc/rrsh/rrsh.json`. Without one, rrsh refuses to start. See [Configuration](#configuration) below for the schema, or copy and edit the shipped example - more worked examples live in [examples/](examples/) (e.g. [`examples/rrsh.ntfy.json`](examples/rrsh.ntfy.json) for an ntfy diagnostic host, [`examples/rrsh.ntfy-stats.json`](examples/rrsh.ntfy-stats.json) for a Prometheus stats host):

   ```bash
   sudo cp /etc/rrsh/rrsh.json.example /etc/rrsh/rrsh.json
   sudo $EDITOR /etc/rrsh/rrsh.json
   ```
   
I highly recommend letting your favorite AI tailor the `rrsh.json` config for you. A good prompt would be:

> I'd like to create a `rrsh.json` config file (see github.com/binwiederhi/rrsh) for <host> that allows
> diagnosing general system health, as well as log file analysis for nginx, grafana, etc.  

3. **Install the AI's authorized key.** The `rrsh` user is created by default. You don't have to use it, but it's the easiest. Simply add your AI's SSH public key to `/var/lib/rrsh/.ssh/authorized_keys`.

   ```bash
   echo 'ssh-ed25519 AAAA... ai-agent-key' | sudo tee -a /var/lib/rrsh/.ssh/authorized_keys
   ```

That's the entire setup - the `rrsh` user can now log in via SSH and reach the JSON-RPC server.

4. **Optional: Run commands as root**:  If any of your rules use `"as": ["root", ...]`, you also need to uncomment the grant line in `/etc/sudoers.d/rrsh` (shipped commented-out):

```bash
sudo sed -i 's/^# rrsh /rrsh /' /etc/sudoers.d/rrsh
```

Without the uncommented sudoers grant, the spawned sudo fails and the error surfaces in `result.stderr`. See [Elevation](#elevation) for the full picture.

## Configuration

`/etc/rrsh/rrsh.json`:

```json
{
  "instructions": "You are on the ntfy production server. The `list_commands.commands` array above lists what is permitted. Most commands run as the SSH user; the systemctl restart rules require as=root.",
  "commands": [
    { "command": ["/usr/bin/whoami"],
      "description": "Show the effective username." },

    { "command": ["/usr/bin/journalctl", "-u", ".+"],
      "description": "Show the journal for a systemd unit." },

    { "command": ["/usr/bin/ping", "-c", "\\d+", ".+"], "timeout": "60s",
      "description": "Ping a host a fixed number of times. Allowed up to 60s." },

    { "command": ["/bin/systemctl", "restart", "ntfy"], "as": ["root"],
      "description": "Restart the ntfy systemd unit." },

    { "command": ["/bin/journalctl", "-fu", "ntfy"], "as": ["$USER", "root"],
      "description": "Follow the ntfy unit log." }
  ]
}
```

Top-level fields:

| Field          | Default     | Meaning                                                                                                       |
| -------------- | ----------- | ------------------------------------------------------------------------------------------------------------- |
| `instructions` | empty       | Returned in `list_commands.instructions` - the canonical place to give Claude host-specific context. The AI reads this on first contact before doing anything else. Treat it like a system prompt scoped to this host. |
| `commands`     | required    | Array of allowlist rules.                                                                                     |

There is no top-level `timeout`. Every command runs with a fixed 30-second deadline unless its rule sets its own `timeout`. Letting operators raise the global timeout would silently let runaway commands hold the JSON-RPC channel hostage; per-rule overrides preserve that guardrail while letting genuinely long-running diagnostics (e.g. `ping -c 100 host`) opt out.

Fields on each command entry:

| Field         | Default       | Meaning                                                                          |
| ------------- | ------------- | -------------------------------------------------------------------------------- |
| `command`     | required      | List of regexes (length ≥ 1). Element 0 matches the binary path; elements 1..N-1 match argv 1-for-1. A call passes only if path matches command[0] AND argv has exactly `len(command)-1` elements AND every argv[i] matches command[i+1]. Patterns are auto-anchored. |
| `timeout`     | `"30s"`       | Per-command timeout, e.g. `"60s"`. Overrides the built-in 30-second default.     |
| `as`          | `["$USER"]`   | Users the command may run as. `$USER` resolves to the SSH user at runtime. Other entries must be valid POSIX login names. |
| `description` | empty         | Free-text shown to Claude in `list_commands.commands[*].description`. Treat it like an API doc string. Control characters are stripped before being sent. |

Rules:

- The matcher requires `command[0]` to match the caller-supplied path - the string the AI sent as `argv[0]` in the `run` call (a regex, but most operators write a literal like `"/usr/bin/whoami"`). An additional defense-in-depth check requires that path to start with `/`, so accidentally-permissive regexes can't enable PATH-resolution of relative names.
- `command[0]` can legitimately be a regex when you want one rule to cover related binaries - e.g. `"/usr/bin/(cat|head)"`.
- Argv matching is element-for-element. `["foo", "bar"]` (two argv elements) is structurally distinct from `["foo bar"]` (one element with a space) - the matcher counts elements separately, so an operator's regex written for two args can't be silently fooled by a single joined element. This is the structural guarantee that makes regex-on-argv safe against shell-injection-style attacks.
- Each entry of `command` is wrapped in `^(?:...)$` at parse time. Writing `"ntfy"` is equivalent to writing `"^ntfy$"` - both reject `"ntfy-extra"`.
- **Multiple rules with the same `command[0]` are allowed** and useful. Each rule describes one argv shape; the matcher tries them in declaration order and the first whose shape matches wins. Use this to express alternatives like `ps aux` vs `ps -ef` vs `ps -eo <fmt>`.
- Unknown JSON fields are rejected - typos in the config fail fast rather than silently weakening the policy.

## How it works

- Installed as a user's login shell. sshd authenticates the SSH client and execs `/usr/bin/rrsh` with the connection's stdio.
- rrsh reads newline-delimited JSON-RPC 2.0 requests on stdin and writes responses on stdout. No shell-string parsing, no `-c` mode.
- Three methods are exposed: `list_commands` (host-specific instructions and the full allowlist), `run_command` (one allowlisted command), and `run_pipeline` (chained stages with native Go pipes).
- Arguments are passed as a real `argv` array - quoting, embedded spaces, and literal metacharacters in argument *values* are not a parser concern.
- Commands are matched against `/etc/rrsh/rrsh.json` rules (fixed path - both the server and the privileged `rrsh sudo` subcommand read the same file, so they cannot disagree about the allowlist). Each rule is a list of regexes - element 0 matches the binary path, elements 1..N-1 match argv 1-for-1 - plus an optional per-command timeout and a list of users the command may run as.
- Allow/deny decisions go to syslog (`auth.info` / `auth.warning`).

## Wire format

Plain JSON-RPC 2.0 over NDJSON. Send one request per line on stdin, get one response per line on stdout. Three methods, no notifications required, no initialize handshake.

```text
{"jsonrpc":"2.0","id":1,"method":"list_commands"}
{"jsonrpc":"2.0","id":2,"method":"run_command","params":{"argv":["/usr/bin/whoami"]}}
```

Server-side refusals (matcher denial, elevation disabled, oversize pipeline) come back as the JSON-RPC `error` envelope with application code `-32000`. A child process's own non-zero exit is **not** an RPC error - it lives in `result.exit`. `run_command` and `run_pipeline` return the same `result` shape.

### `list_commands`

No params. Returns `{instructions, commands}`. `instructions` is the host-specific guidance an operator put in the config - Claude should read it first. `commands` is the full allowlist: each entry is `{command, as, description?, timeout_seconds?}` where `command` is the operator-authored regex list (element 0 = path regex, elements 1..N-1 = argv regexes). One round-trip is enough to discover everything.

### `run_command`

Runs one allowlisted command:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "run_command",
  "params": {
    "argv": ["/usr/bin/journalctl", "-u", "ntfy", "-n", "100"],
    "as": "root",
    "stdin": "optional input"
  }
}
```

`argv[0]` must be an absolute path. `as` requests a target user (must be in the matched rule's `as:` list). `stdin` (optional) is fed to the child on its stdin.

### `run_pipeline`

Chains stages with native Go pipes (no shell). Stdout of stage *i* is wired to stdin of stage *i+1*. Each stage is independently matched against the allowlist and authorized for its `as` user. Per-stage `as` lets an elevated stage feed an unprivileged filter.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "run_pipeline",
  "params": {
    "pipeline": [
      { "argv": ["/usr/bin/journalctl", "-u", "ntfy", "-n", "1000"], "as": "root" },
      { "argv": ["/usr/bin/grep", "ERROR"] }
    ],
    "stdin": "optional input fed to stage 0"
  }
}
```

There is no shell, so the user-typed `|` and `>` characters have no meaning anywhere in rrsh. If your config allows `cat` and `grep` separately, the AI gets `cat /var/log/foo | grep error` by sending a two-stage `pipeline` array. There is no quoting concern: a literal pipe character inside an argument value (e.g. `grep "|"`) is just a byte in an argv element, not a metacharacter.

**Return value (both methods):** structured JSON in the `result` field:

```json
{ "stdout": "...", "stderr": "...", "exit": 0, "timed_out": false, "truncated": false }
```

- Stdout and stderr are captured separately. They are returned as UTF-8 strings, with invalid bytes replaced by U+FFFD.
- Each stream is capped at 10 MB; further bytes are dropped and `truncated: true` is set.
- Exit code is the child's exit (or last stage's exit for a pipeline). Timeouts return exit `124` with `timed_out: true`.

## Elevation

When a rule's `as` list contains a user other than `$USER`, Claude must request that user explicitly by passing `"as": "<user>"` on the `run_command` call (or per-stage in `run_pipeline`):

| `run_command` params                               | Resolves to                                                                  |
| -------------------------------------------------- | ---------------------------------------------------------------------------- |
| `{argv: [...]}`                                    | run as the SSH user (only valid if the rule's `as` includes `$USER`)         |
| `{argv: [...], as: "root"}`                        | run as `root` (only valid if `root` is in the rule's `as`)                   |
| `{argv: [...], as: "deploy"}`                      | run as `deploy` (only valid if `deploy` is in the rule's `as`)               |

The AI sees each rule's `as` list in `list_commands.commands[*].as`, so it can pick the right value without guessing. Omitting `as` for a rule that doesn't include `$USER` is a denial.

Internally, rrsh re-execs itself via `/usr/bin/sudo` to perform the privilege transition. That's the only invocation of real sudo. The gate is **the sudoers grant** at `/etc/sudoers.d/rrsh`:

```
rrsh ALL=(root) NOPASSWD: /usr/bin/rrsh sudo *
```

The package ships this line commented out so installing the package opens no elevation path. Uncomment it (see [Optional: Run commands as root](#optional-run-commands-as-root) in Install) to enable. To allow other target users (e.g. `deploy`), change `(root)` to `(root,deploy)` and list every user that appears in any rule's `as:` list. The file is a conffile, so upgrades won't clobber your changes.

If the grant is missing or commented out, the spawned sudo fails (typically with "sudo: a password is required" or "user ... is not allowed to execute ...") and that text surfaces in `result.stderr` with a non-zero `result.exit`.

The privileged half (`rrsh sudo <path> <argv...>`, hidden subcommand) re-reads `/etc/rrsh/rrsh.json` from disk and re-validates the command against the rule's `as` list before executing - it never trusts its caller, does no flag parsing, and takes the originating user from `$SUDO_USER`.

**Trust boundary:** a parser/match bug in rrsh is a root compromise. Keep `as:` lists minimal, keep regexes tight, leave the sudoers grant commented out until you actually need elevation, and prefer one narrow rule per elevated command over a single permissive rule.

## Logging

Decisions go to syslog under the `rrsh` tag, facility `auth`:

```
Mar  5 21:22:01 host rrsh[12345]: ALLOWED: user=rrsh cmd=/usr/bin/whoami
Mar  5 21:22:14 host rrsh[12346]: DENIED: user=rrsh cmd=/bin/sh
Mar  5 21:22:30 host rrsh[12347]: ALLOWED: user=rrsh as=root cmd=/bin/systemctl restart ntfy
Mar  5 21:22:45 host rrsh[12348]: DENIED: user=rrsh as=root cmd=/usr/bin/whoami
Mar  5 21:22:55 host rrsh[12349]: ALLOWED: user=rrsh cmd=/usr/bin/journalctl -u ntfy -n 1000 | /usr/bin/grep ERROR
```

The `as=` field is present only when the executing user differs from the SSH user. Pipelines are logged as the space-joined stages separated by ` | `. On Debian/Ubuntu these typically end up in `/var/log/auth.log`; on RHEL-likes in `/var/log/secure`.

## Build from source

```bash
go build -o rrsh .
```

Requires Go 1.25+. No external dependencies - `go.mod` lists only the module itself.

## License
Made with ❤️ by [Philipp C. Heckel](https://heckel.io).
Licensed under the [Apache License 2.0](LICENSE).
