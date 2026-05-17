# rrsh — really restricted shell

A restricted login shell. Set it as a user's shell and they can only run commands you've put on an allowlist in `/etc/rrsh/rrsh.json`. Everything else is rejected and logged to syslog.

Useful for SSH accounts that should be limited to a small set of read-only diagnostic commands (`systemctl status`, `journalctl -u …`, `tail /var/log/…`, etc.) without giving the user a real shell. Individual commands can additionally be marked runnable as `root` (or any other user) via a single sudoers line — see [Elevation](#elevation).

Zero runtime dependencies (stdlib only). Single static binary.

## How it works

- Invoked as a login shell (`rrsh -c "<cmd>"`) or directly (`rrsh <cmd>`).
- Loads JSON rules from `/etc/rrsh/rrsh.json` (override with `--config=` or `$RRSH_CONFIG`).
- Each rule is an absolute binary path, optionally with a regex that the argument string must match, a per-command timeout, and a list of users the command may run as (default: the SSH user only).
- Input containing shell metacharacters (`| ; & $ \` > ( ) <`) is rejected outright — there is no shell expansion, no pipes, no redirection.
- A leading `sudo` (or `sudo -u USER`) in the input is a rrsh keyword, not a passthrough: rrsh validates that the rule's `as` list permits the target, then re-execs itself via `/usr/bin/sudo` to perform the privilege transition.
- Allowed commands are executed via `exec.CommandContext` with a timeout. Allow/deny decisions are written to syslog (`auth.info` / `auth.warning`).
- Running `rrsh` with no arguments prints the allowlist.

Exit codes: `0` on success, the child's exit code on failure, `124` on timeout, `126` when the command (or the requested target user) is not allowed.

## Install

Download a `.deb` or `.rpm` from the releases page, then:

```bash
sudo dpkg -i rrsh_*.deb
# or
sudo rpm -i rrsh-*.rpm
```

This installs the binary to `/usr/bin/rrsh` and an example config to `/etc/rrsh/rrsh.json`.

## Use as a login shell

1. Edit `/etc/rrsh/rrsh.json` with the commands you want to allow (see below).
2. Register the shell:
   ```bash
   echo /usr/bin/rrsh | sudo tee -a /etc/shells
   ```
3. Assign it to a user:
   ```bash
   sudo chsh -s /usr/bin/rrsh alice
   ```

When `alice` logs in via SSH and runs `ssh alice@host whoami`, sshd invokes `rrsh -c "whoami"`. If `/usr/bin/whoami` is in the allowlist, it runs; otherwise it's denied.

## Configuration

`/etc/rrsh/rrsh.json`:

```json
{
  "timeout": "10s",
  "commands": [
    { "path": "/usr/bin/whoami" },
    { "path": "/usr/bin/uname",      "args": "^-a$" },
    { "path": "/usr/bin/ps",         "args": "^(aux|-ef)$" },
    { "path": "/usr/bin/ping",       "args": "^-c \\d+ .+$", "timeout": "30s" },

    { "path": "/bin/systemctl",      "args": "^restart ntfy$",     "as": ["root"] },
    { "path": "/usr/bin/journalctl", "args": "^-u ntfy( -f)?$",    "as": ["self", "root"] },
    { "path": "/bin/deploy.sh",                                    "as": ["self", "deploy"] }
  ]
}
```

Fields on each command entry:

| Field     | Default          | Meaning                                                                          |
| --------- | ---------------- | -------------------------------------------------------------------------------- |
| `path`    | required         | Absolute path to the binary.                                                     |
| `args`    | any args allowed | Regex the argument string must match (anchored as written).                      |
| `timeout` | global timeout   | Per-command timeout, e.g. `"30s"`. Overrides the top-level `timeout`.            |
| `as`      | `["self"]`       | Users the command may run as. `self` resolves to the SSH user at runtime.        |

Rules:

- Paths must be absolute. Relative paths and shell builtins are rejected.
- The args regex matches the entire argument string after the binary name (no shell tokenization beyond whitespace splitting).
- Rules are matched in order; the first matching binary path decides the outcome (if its args regex doesn't match, the command is denied — later rules with the same path are not tried).
- Unknown JSON fields are rejected — typos in the config fail fast rather than silently weakening the policy.

## Elevation

When a rule's `as` list contains a user other than `self`, the SSH user can request that target with a `sudo` keyword in their command:

| Typed at SSH                       | Resolves to                                       |
| ---------------------------------- | ------------------------------------------------- |
| `whoami`                           | run as the SSH user (default)                     |
| `sudo whoami`                      | run as `root` (if `root` is in the rule's `as`)   |
| `sudo -u deploy /bin/deploy.sh`    | run as `deploy`                                   |
| `/bin/systemctl restart ntfy`      | implicit `root` for single-target rules           |

The `sudo` here is a rrsh keyword, not `/usr/bin/sudo`. rrsh strips it, checks the rule allows the requested target, then re-execs itself via `/usr/bin/sudo` to actually change user. That's the only invocation of real sudo and it is gated by one sudoers line:

```
# /etc/sudoers.d/alice
alice ALL=(root,deploy) NOPASSWD: /usr/bin/rrsh sudo *
```

List every target user that appears in any rule's `as` list in the `(...)` part. The privileged half of rrsh (`rrsh sudo <cmd>`, hidden subcommand) re-reads `/etc/rrsh/rrsh.json` from disk and re-validates the command against the rule's `as` list before executing — it never trusts its caller, does no flag parsing, and takes the originating user from `$SUDO_USER`.

**Trust boundary:** with this design, a parser/match bug in rrsh is a root compromise. Keep `as` lists minimal, keep args regexes tight, and prefer one narrow rule per elevated command over a single permissive rule.

## Logging

Decisions go to syslog under the `rrsh` tag, facility `auth`:

```
Mar  5 21:22:01 host rrsh[12345]: ALLOWED: user=alice cmd=/usr/bin/whoami
Mar  5 21:22:14 host rrsh[12346]: DENIED: user=alice cmd=/bin/sh
Mar  5 21:22:30 host rrsh[12347]: ALLOWED: user=alice as=root cmd=sudo /bin/systemctl restart ntfy
Mar  5 21:22:45 host rrsh[12348]: DENIED: user=alice as=root cmd=sudo /usr/bin/whoami
```

The `as=` field is present only when the executing user differs from the SSH user, making elevated calls easy to filter. On Debian/Ubuntu these typically end up in `/var/log/auth.log`; on RHEL-likes in `/var/log/secure`.

## Build from source

```bash
go build -o rrsh .
```

Requires Go 1.25+. No CGO, no external dependencies — `go.mod` lists only the module itself.

## License

Apache 2.0
