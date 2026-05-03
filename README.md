# noshell

A restricted login shell. Set it as a user's shell and they can only run commands you've put on an allowlist in `/etc/noshell/noshell.yml`. Everything else is rejected and logged to syslog.

Useful for SSH accounts that should be limited to a small set of read-only diagnostic commands (`systemctl status`, `journalctl -u …`, `tail /var/log/…`, etc.) without giving the user a real shell.

## How it works

- Invoked as a login shell (`noshell -c "<cmd>"`) or directly (`noshell <cmd>`).
- Loads YAML rules from `/etc/noshell/noshell.yml` (override with `--config=` or `$NOSHELL_CONFIG`).
- Each rule is an absolute binary path, optionally with a regex that the argument string must match, and an optional per-command timeout.
- Input containing shell metacharacters (`| ; & $ \` > ( ) <`) is rejected outright — there is no shell expansion, no pipes, no redirection.
- Allowed commands are executed via `exec.CommandContext` with a timeout. Allow/deny decisions are written to syslog (`auth.info` / `auth.warning`).
- Running `noshell` with no arguments prints the allowlist.

Exit codes: `0` on success, the child's exit code on failure, `124` on timeout, `126` when the command is not allowed.

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
  # Bare path: any args allowed
  - /usr/bin/whoami

  # Path with args regex: argument string must match
  - /usr/bin/uname: "^-a$"
  - /usr/bin/ps: "^(aux|-ef)$"

  # Path with args regex AND custom timeout
  - /usr/bin/ping: { args: "^-c \\d+ .+$", timeout: 30s }

  # Logs (read-only)
  - /usr/bin/tail: "^-n \\d+ /var/log/(ntfy|nginx)/.*$"
  - /usr/bin/journalctl: "^-u (ntfy|nginx)$"

  # systemd status only
  - /usr/bin/systemctl: "^(status|is-active) (ntfy|nginx)$"
```

Rules:

- Paths must be absolute. Relative paths and shell builtins are rejected.
- The args regex matches the entire argument string after the binary name (no shell tokenization beyond whitespace splitting).
- Rules are matched in order; the first matching binary path decides the outcome (if its args regex doesn't match, the command is denied — later rules with the same path are not tried).

## Logging

Decisions go to syslog under the `noshell` tag, facility `auth`:

```
Mar  5 21:22:01 host noshell[12345]: ALLOWED: user=alice cmd=/usr/bin/whoami
Mar  5 21:22:14 host noshell[12346]: DENIED: user=alice cmd=/bin/sh
```

On Debian/Ubuntu these typically end up in `/var/log/auth.log`; on RHEL-likes in `/var/log/secure`.

## Build from source

```bash
go build -o noshell .
```

Requires Go 1.25+. No CGO, no external runtime dependencies.

## License

Apache 2.0
