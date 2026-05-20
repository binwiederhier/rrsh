# rrsh — really restricted shell

A JSON-RPC server that exposes a curated, allowlisted set of commands to AI agents (Claude, mostly) over SSH stdin. Installs as a user's login shell so sshd handles auth and transport. Zero external runtime dependencies — Go stdlib only.

## What it is

- An MCP-compatible JSON-RPC 2.0 server speaking NDJSON over stdio.
- Two tools: `list_commands` and `run_command`. `run_command` accepts a single `argv` array or a `pipeline` of stages.
- All arguments are passed as real `argv` slices — no shell tokenization anywhere on the trust boundary.
- Designed to be reachable as `ssh user@host` from a Claude session. Either register as an MCP server (`claude mcp add rrsh-prod -- ssh -T rrsh@host`) or drop "you can diagnose this host via ssh rrsh@host" into the prompt; rrsh self-describes via the `initialize.instructions` field and an instructive rejection on shell-mode attempts.

## Layout

| Directory | Purpose |
|-----------|---------|
| `cmd/` | CLI entry point. `cmd/root.go` dispatches; `cmd/run.go` is the JSON-RPC server entry; `cmd/sudo.go` is the privileged half (`rrsh sudo …`). |
| `config/` | JSON config parser. Strict — `DisallowUnknownFields` everywhere. |
| `matcher/` | (path, argv) → rule lookup. Argv-native, regex on the joined args string. |
| `executor/` | Runs single commands or native Go pipelines. Captures stdout/stderr into capped buffers. |
| `mcp/` | MCP server: NDJSON framing, JSON-RPC 2.0 envelope, two-tool API. |
| `logger/` | Syslog wrapper for `auth.info`/`auth.warning` ALLOWED/DENIED records. |
| `pkg/` | Files that the package installs, mirroring their destination paths. `pkg/etc/rrsh/rrsh.json.example`, `pkg/etc/sudoers.d/rrsh`. |
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
2. The top-level `"sudo": true` flag in `rrsh.json` (default `false`). Without it, the MCP server denies elevated calls and the privileged half exits before running anything.

## Conventions

- All source files end with a newline.
- Comments explain *why*, not *what*; identifiers carry the *what*.
- The privileged half deliberately depends on as few packages as possible. Small duplications (e.g. `resolveAllowedUsers` exists in both `cmd/sudo.go` and `mcp/server.go`) are kept rather than introducing imports that pull JSON-RPC code into the root trust boundary.
- `errors.Is`/`errors.As` for sentinel-error checks (not `err == io.EOF`).
- Tests are colocated with the package they test.

## MCP spec note

`ProtocolVersion` in `mcp/types.go` is the MCP wire-protocol version (e.g. `"2025-03-26"`), defined by the MCP spec — *not* the rrsh binary version. Clients reject unknown values. Bump this when adopting a newer MCP spec, not on rrsh releases. The binary version lives in `serverInfo.version`, populated by main from goreleaser's ldflags.

## Threat model and limits

- **Per-stream output cap**: 10 MiB (executor.MaxOutputBytes); excess silently dropped with `truncated: true`.
- **Per-request size cap**: 1 MiB (mcp.MaxRequestBytes); oversized lines are drained and rejected with a JSON-RPC parse error so the connection survives.
- **No concurrency**: requests are serialized per-connection. Streaming output (e.g. `journalctl -f`) is deliberately not supported in v1 — collect-and-return only.
- **Login-shell only**: no TCP listener, no daemon. Auth is sshd's problem.
