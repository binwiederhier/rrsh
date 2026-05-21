# rrsh - really restricted shell

A JSON-RPC server that exposes a curated, allowlisted set of commands to AI agents (Claude, mostly) over SSH stdin. Installs as a user's login shell so sshd handles auth and transport. Zero external runtime dependencies - Go stdlib only.

## What it is

- A plain JSON-RPC 2.0 server speaking NDJSON over stdio. Not MCP - see [Wire format note](#wire-format-note).
- Two methods: `hello` (instructions and the full allowlist in one round-trip) and `run` (executes one `argv` or a `pipeline`).
- All arguments are passed as real `argv` slices - no shell tokenization anywhere on the trust boundary.
- Designed to be reachable as `ssh -T rrsh@host` from a Claude session. The intended deployment story is to mention rrsh hosts in CLAUDE.md (no client-side MCP registration); rrsh self-describes via the `hello.instructions` field and an instructive rejection on shell-mode attempts.

## Layout

| Directory | Purpose |
|-----------|---------|
| `cmd/` | CLI entry point. `cmd/root.go` dispatches; `cmd/serve.go` is the JSON-RPC server entry (`runServe`); `cmd/sudo.go` is the privileged half (`runSudo`, invoked as `rrsh sudo …`). |
| `config/` | JSON config parser. Strict - `DisallowUnknownFields` everywhere. |
| `matcher/` | (path, argv) → rule lookup. Per-element regex (path = command[0], argv[i] = command[i+1]). |
| `exec/` | Runs single commands or native Go pipelines. `exec/exec.go` holds the `Execer` type and methods; `exec/types.go` holds the package-private consts (`defaultTimeout`, `maxOutputBytes`, `timeoutExitCode`) and exported `Stage`/`Result` types. Captures stdout/stderr via `util.CappedBuffer`. |
| `server/` | JSON-RPC 2.0 server: NDJSON framing, two-method API (`hello`/`run`). `server/types.go` holds wire types + JSON-RPC error codes; `server/server.go` holds the dispatch loop and handlers. |
| `logger/` | Syslog wrapper for `auth.info`/`auth.warning` ALLOWED/DENIED records. |
| `util/` | Tiny stdlib-only helpers. Currently just `util/buffer.go` (`CappedBuffer`, used by `exec` for bounded subprocess output). |
| `pkg/` | Files that the package installs, mirroring their destination paths. `pkg/etc/rrsh/rrsh.json.example`, `pkg/etc/sudoers.d/rrsh`, `pkg/var/lib/rrsh/.hushlogin`. |
| `scripts/` | dpkg maintainer scripts (`postinst.sh`, `postrm.sh`). |
| `dist/` | goreleaser output (not committed). |

## Build commands

```bash
go build -o rrsh .             # local dev binary
go test ./...                  # unit tests
go vet ./...                   # static checks
staticcheck ./...              # extra static analysis (if installed)
deadcode ./...                 # find unreachable code (if installed)
goreleaser release --snapshot --clean --skip=publish  # produce .deb/.rpm in dist/
```

## Trust boundary

A parser/match bug in rrsh is a root compromise. The privileged half (`cmd/sudo.go`) re-reads `/etc/rrsh/rrsh.json` from disk and re-validates the rule's `as:` list before executing anything; it never trusts its caller.

Two independent knobs gate elevation:

1. The sudoers grant at `/etc/sudoers.d/rrsh` (shipped by the package, conffile).
2. The top-level `"sudo": true` flag in `rrsh.json` (default `false`). Without it, the JSON-RPC server denies elevated calls and the privileged half exits before running anything.

The privileged half (`cmd/sudo.go`) deliberately does NOT import `server/`. That keeps the JSON-RPC code out of the root-compromise blast radius. `matcher/` is shared between the two halves because it is small, pure, and has no I/O.

## Conventions

- All source files end with a newline.
- Comments explain *why*, not *what*; identifiers carry the *what*.
- The privileged half deliberately depends on as few packages as possible. Shared pure helpers (e.g. `util.JoinForLog` for audit-log formatting) live in `util/` so both `cmd/sudo.go` and `server/` import the same implementation without pulling JSON-RPC code into the root trust boundary.
- User-identity lookups (`os/user.Current()`) happen at the cmd-layer entry points only - the application fails closed if it cannot determine the current user. Lower-level packages take `username string` as a parameter instead of doing their own lookups.
- Config schema: `command` is a list of regexes. Element 0 matches the binary path, elements 1..N-1 match argv 1-for-1. Multiple rules can share a `command[0]` to express alternative argv shapes; the matcher tries them in declaration order.
- `errors.Is`/`errors.As` for sentinel-error checks (not `err == io.EOF`).
- Tests are colocated with the package they test.

## Wire format note

rrsh used to expose an MCP-compatible surface (`initialize`/`tools/list`/`tools/call`). It no longer does. The protocol is now plain JSON-RPC 2.0 over NDJSON with two methods: `hello` and `run`. The `hello` response carries the host-specific `instructions` *and* the full allowlist - one round-trip gives the AI everything it needs. Server-side refusals (matcher denial, elevation disabled, oversize pipeline) use the JSON-RPC `error` envelope with code `-32000`. A child process's own non-zero exit is **not** an RPC error - it lives in `result.exit`. See the README's "Wire format" section for the wire shape.

The binary's own version (from goreleaser's ldflags) is only used by `rrsh --version` and is not exposed over the JSON-RPC wire.

## Threat model and limits

- **Per-stream output cap**: 10 MiB (`exec.maxOutputBytes`); excess silently dropped with `truncated: true`.
- **Per-request size cap**: 1 MiB (`server.maxRequestBytes`); oversized lines are drained and rejected with a JSON-RPC parse error so the connection survives.
- **Per-command timeout**: 30 s (`exec.defaultTimeout`), or whatever the matched rule specifies. There is no top-level config knob.
- **No concurrency**: requests are serialized per-connection. Streaming output (e.g. `journalctl -f`) is deliberately not supported in v1 - collect-and-return only.
- **Login-shell only**: no TCP listener, no daemon. Auth is sshd's problem.
