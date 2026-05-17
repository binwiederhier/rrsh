# noshell

A restricted login shell. Set it as a user's shell and they can only run commands you've put on an allowlist in `/etc/noshell/noshell.yml`. Everything else is rejected and logged to syslog.

Useful for SSH accounts that should be limited to a small set of read-only diagnostic commands (`systemctl status`, `journalctl -u …`, `tail /var/log/…`, etc.) without giving the user a real shell. Individual commands can additionally be marked runnable as `root` (or any other user) via a single sudoers line — see [Elevation](#elevation).

## How it works

- Invoked as a login shell (`noshell -c "<cmd>"`) or directly (`noshell <cmd>`).
- Loads YAML rules from `/etc/noshell/noshell.yml` (override with `--config=` or `$NOSHELL_CONFIG`).
- Each rule is an absolute binary path, optionally with a regex that the argument string must match, a per-command timeout, and a list of users the command may run as (default: the SSH user only).
- Input containing shell metacharacters (`| ; & $ \` > ( ) <`) is rejected outright — there is no shell expansion, no pipes, no redirection.
- A leading `sudo` (or `sudo -u USER`) in the input is a noshell keyword, not a passthrough: noshell validates that the rule's `as:` list permits the target, then re-execs itself via `/usr/bin/sudo` to perform the privilege transition.
- Allowed commands are executed via `exec.CommandContext` with a timeout. Allow/deny decisions are written to syslog (`auth.info` / `auth.warning`).
- Running `noshell` with no arguments prints the allowlist.

Exit codes: `0` on success, the child's exit code on failure, `124` on timeout, `126` when the command (or the requested target user) is not allowed.

## Install

Download a `.deb` or `.rpm` from the releases page, then:

```bash
sudo dpkg -i noshell_*.deb
# or
sudo rpm -i noshell-*.rpm
```

This installs the binary to `/usr/bin/noshell` and an example config to `/etc/noshell/noshell.yml`.

## Use as a login shell

1. Edit `/etc/noshell/noshell.yml` with the commands you want to allow (see below).
2. Register the shell:
   ```bash
   echo /usr/bin/noshell | sudo tee -a /etc/shells
   ```
3. Assign it to a user:
   ```bash
   sudo chsh -s /usr/bin/noshell alice
   ```

When `alice` logs in via SSH and runs `ssh alice@host whoami`, sshd invokes `noshell -c "whoami"`. If `/usr/bin/whoami` is in the allowlist, it runs; otherwise it's denied.

## Configuration

`/etc/noshell/noshell.yml`:

```yaml
timeout: 10s  # global default; overridable per command

commands:
  # Bare path: any args allowed, runs as the SSH user
  - /usr/bin/whoami

  # Path with args regex
  - /usr/bin/uname: "^-a$"
  - /usr/bin/ps: "^(aux|-ef)$"

  # Path with args regex AND custom timeout
  - /usr/bin/ping: { args: "^-c \\d+ .+$", timeout: 30s }

  # Always runs as root (single-target rules auto-elevate, so the user can
  # type either `systemctl restart ntfy` or `sudo systemctl restart ntfy`)
  - /bin/systemctl: { args: "^restart ntfy$", as: [root] }

  # User picks: `journalctl -u ntfy` (as self) or `sudo journalctl -u ntfy` (as root)
  - /usr/bin/journalctl: { args: "^-u ntfy( -f)?$", as: [self, root] }

  # Multiple non-self targets require explicit `sudo -u USER`
  - /bin/deploy.sh: { as: [self, deploy] }
```

Rules:

- Paths must be absolute. Relative paths and shell builtins are rejected.
- The args regex matches the entire argument string after the binary name (no shell tokenization beyond whitespace splitting).
- Rules are matched in order; the first matching binary path decides the outcome (if its args regex doesn't match, the command is denied — later rules with the same path are not tried).
- `as:` is always a list. Defaults to `[self]` when omitted. `self` resolves to the SSH user at runtime; other entries are real usernames.

## Elevation

When a rule's `as:` list contains a user other than `self`, the SSH user can request that target with a `sudo` keyword in their command:

| Typed at SSH                       | Resolves to                                       |
| ---------------------------------- | ------------------------------------------------- |
| `whoami`                           | run as the SSH user (default)                     |
| `sudo whoami`                      | run as `root` (if `root` is in the rule's `as:`)  |
| `sudo -u deploy /bin/deploy.sh`    | run as `deploy`                                   |
| `/bin/systemctl restart ntfy`      | implicit `root` for single-target rules           |

The `sudo` here is a noshell keyword, not `/usr/bin/sudo`. noshell strips it, checks the rule allows the requested target, then re-execs itself via `/usr/bin/sudo` to actually change user. That's the only invocation of real sudo and it is gated by one sudoers line:

```
# /etc/sudoers.d/alice
alice ALL=(root,deploy) NOPASSWD: /usr/bin/noshell sudo *
```

List every target user that appears in any rule's `as:` list in the `(...)` part. The privileged half of noshell (`noshell sudo <cmd>`, hidden subcommand) re-reads `/etc/noshell/noshell.yml` from disk and re-validates the command against the rule's `as:` list before executing — it never trusts its caller, ignores `--config`, and takes the originating user from `$SUDO_USER`.

**Trust boundary:** with this design, a parser/match bug in noshell is a root compromise. Keep `as:` lists minimal, keep args regexes tight, and prefer one narrow rule per elevated command over a single permissive rule.

## Logging

Decisions go to syslog under the `noshell` tag, facility `auth`:

```
Mar  5 21:22:01 host noshell[12345]: ALLOWED: user=alice cmd=/usr/bin/whoami
Mar  5 21:22:14 host noshell[12346]: DENIED: user=alice cmd=/bin/sh
Mar  5 21:22:30 host noshell[12347]: ALLOWED: user=alice as=root cmd=sudo /bin/systemctl restart ntfy
Mar  5 21:22:45 host noshell[12348]: DENIED: user=alice as=root cmd=sudo /usr/bin/whoami
```

The `as=` field is present only when the executing user differs from the SSH user, making elevated calls easy to filter. On Debian/Ubuntu these typically end up in `/var/log/auth.log`; on RHEL-likes in `/var/log/secure`.

## Build from source

```bash
go build -o noshell .
```

Requires Go 1.25+. No CGO, no external runtime dependencies.

## License

Apache 2.0
