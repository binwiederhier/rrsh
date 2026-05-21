# rrsh — really restricted shell

A JSON-RPC server that lets an AI agent (Claude, mostly) run a curated set of commands on a remote host. Installs as a user's login shell so sshd handles auth and transport — no daemon to keep running, no port to firewall, no auth code in rrsh itself.

Useful for letting Claude SSH into a server and do bounded diagnostic work (`systemctl status`, `journalctl -u …`, `tail /var/log/…`, etc.) without giving it a real shell. Individual commands can additionally be marked runnable as `root` (or any other user) via a single sudoers line — see [Elevation](#elevation).

Zero runtime dependencies (Go stdlib only). Single static binary.

## How it works

- Installed as a user's login shell. sshd authenticates the SSH client and execs `/usr/bin/rrsh` with the connection's stdio.
- rrsh reads newline-delimited JSON-RPC 2.0 requests on stdin and writes responses on stdout. No shell-string parsing, no `-c` mode.
- Three methods are exposed: `hello` (host name, version, instructions), `list` (the allowlist), and `run` (executes one command or a pipeline of them).
- Arguments are passed as a real `argv` array — quoting, embedded spaces, and literal metacharacters in argument *values* are not a parser concern.
- Commands are matched against `/etc/rrsh/rrsh.json` rules (override with `--config=` or `$RRSH_CONFIG`). Each rule is a list of regexes — element 0 matches the binary path, elements 1..N-1 match argv 1-for-1 — plus an optional per-command timeout and a list of users the command may run as.
- Allow/deny decisions go to syslog (`auth.info` / `auth.warning`).

## Install

Download a `.deb` or `.rpm` from the releases page, then:

```bash
sudo dpkg -i rrsh_*.deb
# or
sudo rpm -i rrsh-*.rpm
```

The package installs:

| Path | Notes |
|------|-------|
| `/usr/bin/rrsh` | the binary |
| `/etc/rrsh/rrsh.json.example` | reference config — copy to `/etc/rrsh/rrsh.json` to activate |
| `/etc/sudoers.d/rrsh` | sudoers grant for elevation (conffile, mode `0440`, validated by `visudo` at install) |
| `/var/lib/rrsh/` | package-owned home dir for the `rrsh` user; ships with `.hushlogin` so sshd doesn't prepend its motd/last-login banner to JSON-RPC output |

The postinst script also:

- Adds `/usr/bin/rrsh` to `/etc/shells`.
- Creates the `rrsh` system user with `/usr/bin/rrsh` as its login shell and `/var/lib/rrsh` as its home directory. The password is locked — the account is SSH-key-only.

**Note:** the package does *not* install a default `/etc/rrsh/rrsh.json`. Without one, rrsh refuses to start. Copy the shipped example to activate:

```bash
sudo cp /etc/rrsh/rrsh.json.example /etc/rrsh/rrsh.json
sudo $EDITOR /etc/rrsh/rrsh.json
```

## Set up the SSH key

The `rrsh` user's home (`/var/lib/rrsh`) is owned by root and contains no `authorized_keys` yet. Add Claude's (or another AI agent's) public key:

```bash
sudo install -d -m 755 /var/lib/rrsh/.ssh
sudo install -m 644 /path/to/key.pub /var/lib/rrsh/.ssh/authorized_keys
```

The home dir is intentionally root-owned: the `rrsh` user cannot modify its own `authorized_keys`, only the operator can. That's the entire setup — the `rrsh` user can now log in via SSH and reach the JSON-RPC server.

## Connect from Claude

Just tell Claude how to reach the host. No MCP registration, no client-side config — Claude SSHs ad-hoc using its bash tool. Drop a one-liner into the session or your `CLAUDE.md`:

> You can diagnose this host via `ssh -T rrsh@prod.example.com`. It speaks newline-delimited JSON-RPC 2.0 over stdin: call `hello` first to read host-specific instructions, then `list` for the allowed commands, then `run`.

The first time Claude tries something shell-shaped like `ssh rrsh@prod.example.com whoami`, rrsh refuses with an instructive message that shows the right shape. From there Claude calls `hello`, reads the `instructions` field for host context, and proceeds. This scales to N hosts without N client-side entries — the discoverability is in the protocol, not in your config.

## Configuration

`/etc/rrsh/rrsh.json`:

```json
{
  "name": "ntfy-prod-1",
  "instructions": "You are on the ntfy production server. Call `list` to see what is permitted. Most commands run as the SSH user; the systemctl restart rules require as=root.",
  "sudo": true,
  "commands": [
    { "command": ["/usr/bin/whoami"],
      "description": "Show the effective username." },

    { "command": ["/usr/bin/journalctl", "-u", ".+"],
      "description": "Show the journal for a systemd unit." },

    { "command": ["/usr/bin/ping", "-c", "\\d+", ".+"], "timeout": "60s",
      "description": "Ping a host a fixed number of times. Allowed up to 60s." },

    { "command": ["/bin/systemctl", "restart", "ntfy"],  "as": ["root"],
      "description": "Restart the ntfy systemd unit." },

    { "command": ["/bin/journalctl", "-fu", "ntfy"],     "as": ["self", "root"],
      "description": "Follow the ntfy unit log." }
  ]
}
```

Top-level fields:

| Field          | Default     | Meaning                                                                                                       |
| -------------- | ----------- | ------------------------------------------------------------------------------------------------------------- |
| `name`         | `"rrsh"`    | Reported in `hello.name`. Useful for identifying which host Claude is connected to.                           |
| `instructions` | empty       | Returned in `hello.instructions` — the canonical place to give Claude host-specific context. The AI reads this on first contact before doing anything else. Treat it like a system prompt scoped to this host. |
| `sudo`         | `false`     | Master switch for elevation. When `false`, every `run` whose target user differs from the SSH user is denied, and the privileged `rrsh sudo` subcommand refuses to run — even if `/etc/sudoers.d/rrsh` is in place. Must be `true` to use any rule whose `as:` list includes a non-self user. |
| `commands`     | required    | Array of allowlist rules.                                                                                     |

There is no top-level `timeout`. Every command runs with a fixed 30-second deadline unless its rule sets its own `timeout`. Letting operators raise the global timeout would silently let runaway commands hold the JSON-RPC channel hostage; per-rule overrides preserve that guardrail while letting genuinely long-running diagnostics (e.g. `ping -c 100 host`) opt out.

Fields on each command entry:

| Field         | Default       | Meaning                                                                          |
| ------------- | ------------- | -------------------------------------------------------------------------------- |
| `command`     | required      | List of regexes (length ≥ 1). Element 0 matches the binary path; elements 1..N-1 match argv 1-for-1. A call passes only if path matches command[0] AND argv has exactly `len(command)-1` elements AND every argv[i] matches command[i+1]. Patterns are auto-anchored. |
| `timeout`     | `"30s"`       | Per-command timeout, e.g. `"60s"`. Overrides the built-in 30-second default.     |
| `as`          | `["self"]`    | Users the command may run as. `self` resolves to the SSH user at runtime. Other entries must be valid POSIX login names. |
| `description` | empty         | Free-text shown to Claude via `list`. Treat it like an API doc string. Control characters are stripped before being sent. |

Rules:

- The matcher requires `command[0]` to match the AI-supplied path (a regex, but most operators write a literal like `"/usr/bin/whoami"`). An additional defense-in-depth check requires the AI's path to start with `/`, so accidentally-permissive regexes can't enable PATH-resolution of relative names.
- `command[0]` can legitimately be a regex when you want one rule to cover related binaries — e.g. `"/usr/bin/(cat|head)"`.
- Argv matching is element-for-element. `["foo", "bar"]` (two argv elements) is structurally distinct from `["foo bar"]` (one element with a space) — the matcher counts elements separately, so an operator's regex written for two args can't be silently fooled by a single joined element. This is the structural guarantee that makes regex-on-argv safe against shell-injection-style attacks.
- Each entry of `command` is wrapped in `^(?:…)$` at parse time. Writing `"ntfy"` is equivalent to writing `"^ntfy$"` — both reject `"ntfy-extra"`.
- **Multiple rules with the same `command[0]` are allowed** and useful. Each rule describes one argv shape; the matcher tries them in declaration order and the first whose shape matches wins. Use this to express alternatives like `ps aux` vs `ps -ef` vs `ps -eo <fmt>`.
- Unknown JSON fields are rejected — typos in the config fail fast rather than silently weakening the policy.

## Wire format

Plain JSON-RPC 2.0 over NDJSON. Send one request per line on stdin, get one response per line on stdout. Three methods, no notifications required, no initialize handshake.

```text
{"jsonrpc":"2.0","id":1,"method":"hello"}
{"jsonrpc":"2.0","id":2,"method":"list"}
{"jsonrpc":"2.0","id":3,"method":"run","params":{"argv":["/usr/bin/whoami"]}}
```

Server-side refusals (matcher denial, elevation disabled, oversize pipeline) come back as the JSON-RPC `error` envelope with application code `-32000`. A child process's own non-zero exit is **not** an RPC error — it lives in `result.exit`.

### `hello`

No params. Returns `{name, version, instructions}`. `instructions` is the host-specific guidance an operator put in the config — Claude should read it first.

### `list`

No params. Returns `{commands: [{command, as, description?, timeout_seconds?}]}`. `command` is the operator-authored regex list (element 0 = path regex, elements 1..N-1 = argv regexes).

### `run`

Executes one allowlisted command, or a pipeline of them. Exactly one of `argv` or `pipeline` must be set.

**Single command:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "run",
  "params": {
    "argv": ["/usr/bin/journalctl", "-u", "ntfy", "-n", "100"],
    "as": "root",
    "stdin": "optional input"
  }
}
```

**Pipeline:** stdout of stage *i* is wired to stdin of stage *i+1* via native Go pipes — no shell is invoked anywhere.

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "run",
  "params": {
    "pipeline": [
      { "argv": ["/usr/bin/journalctl", "-u", "ntfy", "-n", "1000"], "as": "root" },
      { "argv": ["/usr/bin/grep", "ERROR"] }
    ]
  }
}
```

Each stage is independently matched against the allowlist and authorized for its `as` user. Per-stage `as` lets an elevated stage feed an unprivileged filter.

The pipeline field is the only way to compose commands — there is no shell, so the user-typed `|` and `>` characters have no meaning anywhere in rrsh. If your config allows `cat` and `grep` separately, the AI gets `cat /var/log/foo | grep error` by sending a two-stage `pipeline` array. There is no quoting concern: a literal pipe character inside an argument value (e.g. `grep "|"`) is just a byte in an argv element, not a metacharacter.

**Return value:** structured JSON in the `result` field:

```json
{ "stdout": "...", "stderr": "...", "exit": 0, "timed_out": false, "truncated": false }
```

- Stdout and stderr are captured separately. They are returned as UTF-8 strings, with invalid bytes replaced by U+FFFD.
- Each stream is capped at 10 MB; further bytes are dropped and `truncated: true` is set.
- Exit code is the child's exit (or last stage's exit for a pipeline). Timeouts return exit `124` with `timed_out: true`.

## Elevation

When a rule's `as` list contains a user other than `self`, Claude can request that target by passing `"as": "<user>"` on the `run` call (or per-stage in a pipeline):

| `run` params                                       | Resolves to                                       |
| -------------------------------------------------- | ------------------------------------------------- |
| `{argv: [...]}`                                    | run as the SSH user (default)                     |
| `{argv: [...], as: "root"}`                        | run as `root` (if `root` is in the rule's `as`)   |
| `{argv: [...], as: "deploy"}`                      | run as `deploy`                                   |
| `{argv: ["/bin/systemctl","restart","ntfy"]}`      | implicit `root` for single-target rules           |

For rules whose `as` list contains exactly one non-self target, Claude does not need to pass `as` — rrsh implicitly elevates. This is the common "always root" case.

Internally, rrsh re-execs itself via `/usr/bin/sudo` to perform the privilege transition. That's the only invocation of real sudo, and it is gated by two independent knobs:

1. **The sudoers grant**, installed by the package at `/etc/sudoers.d/rrsh`:
   ```
   rrsh ALL=(root) NOPASSWD: /usr/bin/rrsh sudo *
   ```
   To allow other target users (e.g. `deploy`), edit the `(...)` part to list every target user that appears in any rule's `as:` list. The file is a conffile, so upgrades won't clobber your changes.

2. **The config-level `sudo` flag**, default `false`. Until you set `"sudo": true` at the top of `/etc/rrsh/rrsh.json`, the package's sudoers grant has no effect: the JSON-RPC server denies elevated calls with a clear error, and the privileged `rrsh sudo` half exits before running anything.

The privileged half (`rrsh sudo <path> <argv...>`, hidden subcommand) re-reads `/etc/rrsh/rrsh.json` from disk and re-validates the command against the rule's `as` list before executing — it never trusts its caller, does no flag parsing, and takes the originating user from `$SUDO_USER`.

**Trust boundary:** a parser/match bug in rrsh is a root compromise. Keep `as:` lists minimal, keep `args` regexes tight, leave `"sudo": false` until you actually need elevation, and prefer one narrow rule per elevated command over a single permissive rule.

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

## Why JSON-RPC, not shell-string parsing

SSH joins the client's arguments into a single string before sshd ever sees them, so a restricted shell that reads `-c "..."` is fundamentally a string parser. Quoting, embedded spaces, and metacharacters become parser concerns — and any parser bug is on the privileged trust boundary.

rrsh used to be a string parser. It isn't any more. With JSON-RPC, every argument arrives in its own array slot, so input like `grep " | > /dev/null"` is unambiguous: the literal pipe-and-redirect string is one element in `argv`, not a shell metacharacter. The matcher is a per-element regex list; the executor invokes the binary with the original slice. No tokenization layer to write or audit.

If you need a human-friendly CLI on top of this, write one in your client (it can call `run` directly). rrsh's job is to be a safe, structured endpoint.

## Build from source

```bash
go build -o rrsh .
```

Requires Go 1.25+. No CGO, no external dependencies — `go.mod` lists only the module itself.

## License

Apache 2.0
