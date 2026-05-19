package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/executor"
	"github.com/binwiederhier/rrsh/logger"
	"github.com/binwiederhier/rrsh/matcher"
)

// Version is overwritten by main at startup. Default kept for tests.
var Version = "dev"

// SudoBinary is the path to /usr/bin/sudo. Indirection for tests.
var SudoBinary = "/usr/bin/sudo"

// Server holds the dependencies needed to serve JSON-RPC requests.
type Server struct {
	cfg      *config.Config
	matcher  *matcher.Matcher
	executor *executor.Executor
	log      *logger.SyslogLogger
	self     string // current SSH user
	rrshBin  string // path to this binary for elevation re-exec
	in       *bufio.Reader
	out      io.Writer
}

// New constructs a Server. `self` is the current SSH user (what "self"
// resolves to in `as:` lists). `rrshBin` is the path used to re-exec via
// sudo for elevation.
func New(cfg *config.Config, log *logger.SyslogLogger, self, rrshBin string, in io.Reader, out io.Writer) *Server {
	return &Server{
		cfg:      cfg,
		matcher:  matcher.New(cfg.Commands),
		executor: executor.New(cfg.Timeout),
		log:      log,
		self:     self,
		rrshBin:  rrshBin,
		in:       bufio.NewReaderSize(in, 1<<20),
		out:      out,
	}
}

// Serve runs the read/dispatch/write loop until stdin closes. Errors at
// the JSON-RPC envelope level are written back as RPC error responses;
// only an irrecoverable stdin read error stops the loop.
func (s *Server) Serve() error {
	enc := json.NewEncoder(s.out)
	for {
		line, err := s.readLine()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		resp := s.handle(line)
		if resp == nil {
			// Notification (no ID) — by spec we do not respond.
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
}

// readLine reads one NDJSON line. A single request may be up to 1 MB; we
// reuse the underlying reader's buffer.
func (s *Server) readLine() ([]byte, error) {
	line, err := s.in.ReadBytes('\n')
	if err == io.EOF && len(line) > 0 {
		return line, nil
	}
	return line, err
}

// handle parses one request and returns a response (or nil for
// notifications). All parse-time errors get reported as JSON-RPC error
// responses with a null ID.
func (s *Server) handle(data []byte) *response {
	var req request
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return errResponse(nil, ErrParse, "parse error: "+err.Error())
	}
	if req.JSONRPC != "2.0" {
		return errResponse(req.ID, ErrInvalidRequest, "jsonrpc must be \"2.0\"")
	}
	if req.Method == "" {
		return errResponse(req.ID, ErrInvalidRequest, "method is required")
	}
	// Notifications have no ID and never get a reply.
	isNotification := len(req.ID) == 0

	result, rpcErr := s.dispatch(req.Method, req.Params)
	if isNotification {
		return nil
	}
	if rpcErr != nil {
		return &response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
	}
	return &response{JSONRPC: "2.0", ID: req.ID, Result: result}
}

// dispatch routes a parsed request to its handler.
func (s *Server) dispatch(method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return s.handleInitialize(params)
	case "tools/list":
		return s.handleToolsList()
	case "tools/call":
		return s.handleToolsCall(params)
	case "notifications/initialized", "notifications/cancelled":
		// Accepted but no-op.
		return nil, nil
	case "ping":
		return struct{}{}, nil
	default:
		return nil, &rpcError{Code: ErrMethodNotFound, Message: "method not found: " + method}
	}
}

func (s *Server) handleInitialize(_ json.RawMessage) (any, *rpcError) {
	name := s.cfg.Name
	if name == "" {
		name = "rrsh"
	}
	return initializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    serverCapabilities{Tools: &toolsCapability{}},
		ServerInfo:      serverInfo{Name: name, Version: Version},
		Instructions:    s.cfg.Instructions,
	}, nil
}

// listAllowlistSchema is the JSON Schema for the list_commands tool's
// input (no arguments).
var listAllowlistSchema = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

// runCommandSchema is the JSON Schema for the run_command tool's input.
var runCommandSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "argv": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Command + arguments as an argv slice. The first element must be an absolute path. Exactly one of argv or pipeline must be set."
    },
    "pipeline": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "argv": {"type": "array", "items": {"type": "string"}},
          "as": {"type": "string"}
        },
        "required": ["argv"],
        "additionalProperties": false
      },
      "description": "Multi-stage pipeline. Stdout of stage i is wired to stdin of stage i+1. Each stage is independently validated against the allowlist."
    },
    "as": {
      "type": "string",
      "description": "Run as this user (e.g. \"root\"). Must be permitted by the matched rule's as: list. Ignored when pipeline is set — use per-stage as there."
    },
    "stdin": {
      "type": "string",
      "description": "Optional data to pipe into stdin of the first (or only) stage."
    }
  },
  "additionalProperties": false
}`)

func (s *Server) handleToolsList() (any, *rpcError) {
	return toolsListResult{
		Tools: []toolDef{
			{
				Name:        "list_commands",
				Description: "List every command rule this rrsh instance will allow. Returns a JSON array of {path, args_pattern, as, description, timeout_seconds}. Call this first to discover what run_command can execute.",
				InputSchema: listAllowlistSchema,
			},
			{
				Name:        "run_command",
				Description: "Execute one allowlisted command, or a pipeline of them. Pass argv as a string array (the first element is the absolute path); a regex on the rule decides whether the arguments are accepted. Returns structured {stdout, stderr, exit, timed_out, truncated}.",
				InputSchema: runCommandSchema,
			},
		},
	}, nil
}

func (s *Server) handleToolsCall(params json.RawMessage) (any, *rpcError) {
	var p toolsCallParams
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, &rpcError{Code: ErrInvalidParams, Message: "invalid tools/call params: " + err.Error()}
	}
	switch p.Name {
	case "list_commands":
		return s.toolListCommands()
	case "run_command":
		return s.toolRunCommand(p.Arguments)
	default:
		return nil, &rpcError{Code: ErrInvalidParams, Message: "unknown tool: " + p.Name}
	}
}

func (s *Server) toolListCommands() (any, *rpcError) {
	out := make([]allowlistEntry, 0, len(s.cfg.Commands))
	for _, r := range s.cfg.Commands {
		entry := allowlistEntry{
			Path:        r.Path,
			As:          r.As,
			Description: r.Description,
		}
		if r.ArgsPattern != nil {
			entry.ArgsPattern = r.ArgsPattern.String()
		}
		if r.Timeout > 0 {
			entry.TimeoutSecs = r.Timeout.Seconds()
		}
		out = append(out, entry)
	}
	jsonText, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, &rpcError{Code: ErrInternal, Message: err.Error()}
	}
	return toolsCallResult{Content: []contentBlock{{Type: "text", Text: string(jsonText)}}}, nil
}

func (s *Server) toolRunCommand(args json.RawMessage) (any, *rpcError) {
	if len(args) == 0 {
		return nil, &rpcError{Code: ErrInvalidParams, Message: "run_command requires arguments"}
	}
	var a runCommandArgs
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, &rpcError{Code: ErrInvalidParams, Message: "invalid run_command arguments: " + err.Error()}
	}
	hasArgv := len(a.Argv) > 0
	hasPipeline := len(a.Pipeline) > 0
	if hasArgv == hasPipeline {
		return s.deny("run_command requires exactly one of argv or pipeline", ""), nil
	}

	if hasArgv {
		return s.runSingle(a)
	}
	return s.runPipeline(a)
}

// runSingle executes a single-stage run_command call.
func (s *Server) runSingle(a runCommandArgs) (any, *rpcError) {
	path := a.Argv[0]
	argv := a.Argv[1:]

	rule, ok := s.matcher.Match(path, argv)
	if !ok {
		input := joinForLog(path, argv)
		s.log.Denied(input, s.self)
		return s.deny("command not allowed: "+input, ""), nil
	}

	requested := a.As
	if requested == "" {
		requested = config.SelfUser
	}
	target := resolveTarget(requested, rule.As, s.self)
	if target == "" {
		input := joinForLog(path, argv)
		s.log.Denied(input, s.self)
		return s.deny(fmt.Sprintf("%s not permitted to run as %s", input, displayTarget(requested, s.self)), ""), nil
	}

	var stdin io.Reader
	if a.Stdin != "" {
		stdin = strings.NewReader(a.Stdin)
	}

	input := joinForLog(path, argv)
	if target == s.self {
		s.log.Allowed(input, s.self)
		res := s.executor.Execute(path, argv, rule, stdin)
		return runResultToTool(res), nil
	}

	s.log.Allowed(input, target)
	res := s.elevateAndExecute(target, path, argv, rule, stdin)
	return runResultToTool(res), nil
}

// runPipeline executes a multi-stage pipeline.
func (s *Server) runPipeline(a runCommandArgs) (any, *rpcError) {
	if a.As != "" {
		return s.deny("top-level `as` is not valid with pipeline — set `as` per stage", ""), nil
	}

	stages := make([]executor.Stage, 0, len(a.Pipeline))
	for i, step := range a.Pipeline {
		if len(step.Argv) == 0 {
			return s.deny(fmt.Sprintf("pipeline stage %d has empty argv", i), ""), nil
		}
		path := step.Argv[0]
		argv := step.Argv[1:]

		rule, ok := s.matcher.Match(path, argv)
		if !ok {
			input := joinForLog(path, argv)
			s.log.Denied(s.fullPipelineLog(a.Pipeline), s.self)
			return s.deny("pipeline stage not allowed: "+input, ""), nil
		}

		requested := step.As
		if requested == "" {
			requested = config.SelfUser
		}
		target := resolveTarget(requested, rule.As, s.self)
		if target == "" {
			input := joinForLog(path, argv)
			s.log.Denied(s.fullPipelineLog(a.Pipeline), s.self)
			return s.deny(fmt.Sprintf("pipeline stage %s not permitted to run as %s", input, displayTarget(requested, s.self)), ""), nil
		}

		// For elevation in a pipeline, rewrite the stage to invoke
		// /usr/bin/sudo … rrsh sudo <path> <argv>. The privileged half
		// re-validates from disk against the same rule's `as:` list.
		if target != s.self {
			path, argv = s.buildElevatedArgv(target, path, argv)
		}

		var stageStdin io.Reader
		if i == 0 && a.Stdin != "" {
			stageStdin = strings.NewReader(a.Stdin)
		}

		stages = append(stages, executor.Stage{
			Path:  path,
			Argv:  argv,
			Rule:  rule,
			Stdin: stageStdin,
		})
	}

	s.log.Allowed(s.fullPipelineLog(a.Pipeline), s.self)
	res := s.executor.ExecutePipeline(stages)
	return runResultToTool(res), nil
}

// elevateAndExecute re-execs the rrsh binary via /usr/bin/sudo to run the
// command as `target`. The privileged half (cmd/sudo.go) reads its argv
// directly from os.Args, re-loads config from disk, and re-validates the
// rule's `as:` list before executing.
func (s *Server) elevateAndExecute(target, path string, argv []string, rule *config.CommandRule, stdin io.Reader) executor.Result {
	sudoPath, sudoArgv := s.buildElevatedArgv(target, path, argv)
	// Pretend the elevation is just another command — same executor
	// semantics, same timeout.
	return s.executor.Execute(sudoPath, sudoArgv, rule, stdin)
}

// buildElevatedArgv produces (path, argv) suitable for executor.Execute
// to spawn /usr/bin/sudo and re-enter rrsh's privileged half.
//
//	sudo -n [-u TARGET] /usr/bin/rrsh sudo <path> <argv...>
//
// -u is omitted when target == "root" (sudo defaults to root).
func (s *Server) buildElevatedArgv(target, path string, argv []string) (string, []string) {
	out := []string{"-n"}
	if target != "root" {
		out = append(out, "-u", target)
	}
	out = append(out, s.rrshBin, "sudo", path)
	out = append(out, argv...)
	return SudoBinary, out
}

// runResultToTool wraps an executor.Result into the MCP tool response.
func runResultToTool(res executor.Result) toolsCallResult {
	payload := runCommandOutput{
		Stdout:    safeUTF8(res.Stdout),
		Stderr:    safeUTF8(res.Stderr),
		Exit:      res.ExitCode,
		TimedOut:  res.TimedOut,
		Truncated: res.Truncated,
	}
	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return toolsCallResult{
			Content: []contentBlock{{Type: "text", Text: "rrsh: internal: " + err.Error()}},
			IsError: true,
		}
	}
	return toolsCallResult{
		Content: []contentBlock{{Type: "text", Text: string(text)}},
		IsError: res.ExitCode != 0,
	}
}

// deny constructs an error result for a denied call. msg is the
// human-readable reason; detail is appended if non-empty.
func (s *Server) deny(msg, detail string) toolsCallResult {
	if detail != "" {
		msg += ": " + detail
	}
	return toolsCallResult{
		Content: []contentBlock{{Type: "text", Text: "rrsh: " + msg}},
		IsError: true,
	}
}

// fullPipelineLog formats a pipeline as a single space-joined string for
// syslog. Stages are joined with " | " for readability.
func (s *Server) fullPipelineLog(stages []runStep) string {
	parts := make([]string, len(stages))
	for i, st := range stages {
		path := ""
		var rest []string
		if len(st.Argv) > 0 {
			path = st.Argv[0]
			rest = st.Argv[1:]
		}
		parts[i] = joinForLog(path, rest)
	}
	return strings.Join(parts, " | ")
}

func joinForLog(path string, argv []string) string {
	if len(argv) == 0 {
		return path
	}
	return path + " " + strings.Join(argv, " ")
}

func errResponse(id json.RawMessage, code int, msg string) *response {
	if id == nil {
		id = json.RawMessage("null")
	}
	return &response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
}

// safeUTF8 returns b as a string, replacing any invalid UTF-8 sequences
// with U+FFFD. Without this, arbitrary command output containing invalid
// bytes (binary data, stray escape sequences) would force json.Marshal to
// emit bytes that subsequently fail to decode.
func safeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// resolveTarget mirrors the cmd package's resolution: requested user (with
// "self" → s.self), checked against the rule's resolved `as:` list. Returns
// "" on denial. Single-target rules implicitly elevate when the caller did
// not ask for a different user.
func resolveTarget(requested string, allowed []string, self string) string {
	if requested == config.SelfUser {
		requested = self
	}
	resolved := resolveAllowedUsers(allowed, self)
	for _, u := range resolved {
		if u == requested {
			return requested
		}
	}
	if requested == self && len(resolved) == 1 {
		return resolved[0]
	}
	return ""
}

// resolveAllowedUsers expands every "self" entry in `allowed` to `self`.
func resolveAllowedUsers(allowed []string, self string) []string {
	out := make([]string, 0, len(allowed))
	for _, u := range allowed {
		if u == config.SelfUser {
			out = append(out, self)
		} else {
			out = append(out, u)
		}
	}
	return out
}

func displayTarget(requested, self string) string {
	if requested == config.SelfUser {
		return self
	}
	return requested
}

// SelfBinary returns the absolute path to the running rrsh executable.
// Falls back to "/usr/bin/rrsh" if os.Executable fails (shouldn't happen
// in practice on Linux).
func SelfBinary() string {
	p, err := os.Executable()
	if err != nil {
		return "/usr/bin/rrsh"
	}
	return p
}
