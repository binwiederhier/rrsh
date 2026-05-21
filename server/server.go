package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/exec"
	"github.com/binwiederhier/rrsh/logger"
	"github.com/binwiederhier/rrsh/matcher"
)

// Version is the rrsh binary version, set by main at startup.
var Version = "dev"

const (
	sudoBinary = "/usr/bin/sudo"
	// When elevating to root we omit `-u root` to match standard sudo usage.
	userRoot = "root"
	// maxRequestBytes caps a single JSON-RPC line (DoS guard).
	maxRequestBytes = 1 << 20 // 1 MiB
	// maxPipelineStages caps processes spawned per pipeline (DoS guard).
	maxPipelineStages = 16
)

// Server holds the dependencies needed to serve JSON-RPC requests.
type Server struct {
	cfg     *config.Config
	matcher *matcher.Matcher
	log     *logger.SyslogLogger
	self    string // current SSH user
	rrshBin string // path to this binary for elevation re-exec
	in      *bufio.Reader
	out     io.Writer
}

// New constructs a Server. `self` is the current SSH user (what "self"
// resolves to in `as:` lists). `rrshBin` is the path used to re-exec via
// sudo for elevation.
func New(cfg *config.Config, log *logger.SyslogLogger, self, rrshBin string, in io.Reader, out io.Writer) *Server {
	return &Server{
		cfg:     cfg,
		matcher: matcher.New(cfg.Commands),
		log:     log,
		self:    self,
		rrshBin: rrshBin,
		in:      bufio.NewReaderSize(in, 1<<20),
		out:     out,
	}
}

// Serve runs the read/dispatch/write loop until stdin closes. Errors at
// the JSON-RPC envelope level are written back as RPC error responses;
// only an irrecoverable stdin read error stops the loop.
func (s *Server) Serve() error {
	enc := json.NewEncoder(s.out)
	for {
		line, tooLong, err := s.readLine()
		atEOF := errors.Is(err, io.EOF)
		if atEOF && len(line) == 0 {
			return nil
		}
		if err != nil && !atEOF {
			return err
		}
		if tooLong {
			// Surface as a JSON-RPC error rather than a transport
			// failure, then keep serving (the offending line is already
			// discarded by readLine).
			tooLongResp := errResponse(nil, errParse, fmt.Sprintf("request exceeds %d-byte limit", maxRequestBytes))
			if err := enc.Encode(tooLongResp); err != nil {
				return err
			}
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if atEOF {
				return nil
			}
			continue
		}
		resp := s.handle(line)
		if resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
		if atEOF {
			return nil
		}
	}
}

// readLine reads one NDJSON line, capping its length at maxRequestBytes
// to prevent OOM from an unbounded client. The tooLong return is set
// when the cap was hit; in that case the caller should reply with a
// parse error and the remainder of the offending line (up to the next
// '\n') is consumed and discarded so the next request is read cleanly.
func (s *Server) readLine() (line []byte, tooLong bool, err error) {
	for {
		fragment, fragErr := s.in.ReadSlice('\n')
		// ReadSlice returns the buffer's internal slice; copy before
		// appending so the next read can't clobber what we've kept.
		line = append(line, fragment...)
		if errors.Is(fragErr, bufio.ErrBufferFull) {
			// Partial read; loop until we hit '\n' or io.EOF.
			if len(line) > maxRequestBytes {
				tooLong = true
				if drainErr := s.discardToNewline(); drainErr != nil && !errors.Is(drainErr, io.EOF) {
					return nil, true, drainErr
				}
				return nil, true, nil
			}
			continue
		}
		if len(line) > maxRequestBytes {
			tooLong = true
			return nil, true, nil
		}
		return line, false, fragErr
	}
}

// discardToNewline reads and drops bytes from the buffered reader until
// a newline (or EOF). Used to resync the framing after rejecting an
// oversized request line.
func (s *Server) discardToNewline() error {
	for {
		_, err := s.in.ReadSlice('\n')
		if err == nil {
			return nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return err
	}
}

// handle parses one request and returns a response (or nil for
// notifications). All parse-time errors get reported as JSON-RPC error
// responses with a null ID.
func (s *Server) handle(data []byte) *response {
	var req request
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return errResponse(nil, errParse, "parse error: "+err.Error())
	}
	if req.JSONRPC != "2.0" {
		return errResponse(req.ID, errInvalidRequest, "jsonrpc must be \"2.0\"")
	}
	if req.Method == "" {
		return errResponse(req.ID, errInvalidRequest, "method is required")
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
	case "hello":
		return s.handleHello()
	case "list":
		return s.handleList()
	case "run":
		return s.handleRun(params)
	default:
		return nil, &rpcError{Code: errMethodNotFound, Message: "method not found: " + method}
	}
}

func (s *Server) handleHello() (any, *rpcError) {
	name := s.cfg.Name
	if name == "" {
		name = "rrsh"
	}
	return &helloResult{
		Name:         name,
		Version:      Version,
		Instructions: s.cfg.Instructions,
	}, nil
}

func (s *Server) handleList() (any, *rpcError) {
	out := make([]commandEntry, 0, len(s.cfg.Commands))
	for _, r := range s.cfg.Commands {
		entry := commandEntry{
			Command:     r.CommandSource,
			As:          r.As,
			Description: sanitizeDescription(r.Description),
		}
		if r.Timeout > 0 {
			entry.TimeoutSecs = r.Timeout.Seconds()
		}
		out = append(out, entry)
	}
	return &listResult{Commands: out}, nil
}

// handleRun requires exactly one of `argv` or `pipeline`. Matcher
// denials surface as JSON-RPC errors (code errDenied) so the AI client
// gets a clean signal — the child's own non-zero exit lives in
// result.exit, not in the RPC error envelope.
func (s *Server) handleRun(params json.RawMessage) (any, *rpcError) {
	if len(params) == 0 {
		return nil, &rpcError{Code: errInvalidParams, Message: "run requires params"}
	}
	var p runParams
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, &rpcError{Code: errInvalidParams, Message: "invalid run params: " + err.Error()}
	}
	hasArgv := len(p.Argv) > 0
	hasPipeline := len(p.Pipeline) > 0
	if hasArgv == hasPipeline {
		return nil, &rpcError{Code: errInvalidParams, Message: "run requires exactly one of argv or pipeline"}
	}
	if hasArgv {
		return s.runSingle(p)
	}
	return s.runPipeline(p)
}

// runSingle executes a single-argv run call.
func (s *Server) runSingle(p runParams) (any, *rpcError) {
	path := p.Argv[0]
	argv := p.Argv[1:]

	rule, ok := s.matcher.Match(path, argv)
	if !ok {
		input := joinForLog(path, argv)
		s.log.Denied(input, s.self)
		return nil, deny("command not allowed: " + input)
	}

	requested := p.As
	if requested == "" {
		requested = config.SelfUser
	}
	target := resolveTarget(requested, rule.As, s.self)
	if target == "" {
		input := joinForLog(path, argv)
		s.log.Denied(input, s.self)
		return nil, deny(fmt.Sprintf("%s not permitted to run as %s", input, displayTarget(requested, s.self)))
	}

	var stdin io.Reader
	if p.Stdin != "" {
		stdin = strings.NewReader(p.Stdin)
	}

	input := joinForLog(path, argv)
	if target == s.self {
		s.log.Allowed(input, s.self)
		return toRunResult(exec.Execute(path, argv, rule, stdin)), nil
	}

	if !s.cfg.Sudo {
		s.log.Denied(input, s.self)
		return nil, deny("elevation disabled in config (set \"sudo\": true in /etc/rrsh/rrsh.json)")
	}

	s.log.Allowed(input, target)
	return toRunResult(s.elevateAndExecute(target, path, argv, rule, stdin)), nil
}

// runPipeline executes a multi-stage pipeline.
func (s *Server) runPipeline(p runParams) (any, *rpcError) {
	if p.As != "" {
		return nil, &rpcError{Code: errInvalidParams, Message: "top-level `as` is not valid with pipeline — set `as` per stage"}
	}
	if len(p.Pipeline) > maxPipelineStages {
		return nil, deny(fmt.Sprintf("pipeline exceeds %d-stage limit", maxPipelineStages))
	}

	stages := make([]exec.Stage, 0, len(p.Pipeline))
	for i, step := range p.Pipeline {
		if len(step.Argv) == 0 {
			return nil, &rpcError{Code: errInvalidParams, Message: fmt.Sprintf("pipeline stage %d has empty argv", i)}
		}
		path := step.Argv[0]
		argv := step.Argv[1:]

		rule, ok := s.matcher.Match(path, argv)
		if !ok {
			input := joinForLog(path, argv)
			s.log.Denied(s.fullPipelineLog(p.Pipeline), s.self)
			return nil, deny("pipeline stage not allowed: " + input)
		}

		requested := step.As
		if requested == "" {
			requested = config.SelfUser
		}
		target := resolveTarget(requested, rule.As, s.self)
		if target == "" {
			input := joinForLog(path, argv)
			s.log.Denied(s.fullPipelineLog(p.Pipeline), s.self)
			return nil, deny(fmt.Sprintf("pipeline stage %s not permitted to run as %s", input, displayTarget(requested, s.self)))
		}

		// For elevation in a pipeline, rewrite the stage to invoke
		// /usr/bin/sudo … rrsh sudo <path> <argv>. The privileged half
		// re-validates from disk against the same rule's `as:` list.
		if target != s.self {
			if !s.cfg.Sudo {
				s.log.Denied(s.fullPipelineLog(p.Pipeline), s.self)
				return nil, deny("pipeline stage requires elevation but sudo is disabled in config (set \"sudo\": true)")
			}
			path, argv = s.buildElevatedArgv(target, path, argv)
		}

		var stageStdin io.Reader
		if i == 0 && p.Stdin != "" {
			stageStdin = strings.NewReader(p.Stdin)
		}

		stages = append(stages, exec.Stage{
			Path:  path,
			Argv:  argv,
			Rule:  rule,
			Stdin: stageStdin,
		})
	}

	s.log.Allowed(s.fullPipelineLog(p.Pipeline), s.self)
	return toRunResult(exec.ExecutePipeline(stages)), nil
}

// elevateAndExecute re-execs the rrsh binary via /usr/bin/sudo to run the
// command as `target`. The privileged half (cmd/sudo.go) reads its argv
// directly from os.Args, re-loads config from disk, and re-validates the
// rule's `as:` list before executing.
func (s *Server) elevateAndExecute(target, path string, argv []string, rule *config.CommandRule, stdin io.Reader) *exec.Result {
	sudoPath, sudoArgv := s.buildElevatedArgv(target, path, argv)
	// Pretend the elevation is just another command — same executor
	// semantics, same timeout.
	return exec.Execute(sudoPath, sudoArgv, rule, stdin)
}

// buildElevatedArgv produces (path, argv) suitable for exec.Execute to
// spawn /usr/bin/sudo and re-enter rrsh's privileged half.
//
//	sudo -n [-u TARGET] /usr/bin/rrsh sudo <path> <argv...>
//
// -u is omitted when target == "root" (sudo defaults to root).
func (s *Server) buildElevatedArgv(target, path string, argv []string) (string, []string) {
	out := []string{"-n"}
	if target != userRoot {
		out = append(out, "-u", target)
	}
	out = append(out, s.rrshBin, "sudo", path)
	out = append(out, argv...)
	return sudoBinary, out
}

// toRunResult converts the executor's internal Result into the wire shape.
func toRunResult(res *exec.Result) *runResult {
	return &runResult{
		Stdout:    safeUTF8(res.Stdout),
		Stderr:    safeUTF8(res.Stderr),
		Exit:      res.ExitCode,
		TimedOut:  res.TimedOut,
		Truncated: res.Truncated,
	}
}

// deny builds the application-specific "denied" RPC error.
func deny(msg string) *rpcError {
	return &rpcError{Code: errDenied, Message: msg}
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

// logEscaper neutralizes record-terminator chars in argv so an attacker
// can't forge fake ALLOWED/DENIED lines in syslog.
var logEscaper = strings.NewReplacer("\n", "\\n", "\r", "\\r", "\x00", "\\0")

// sanitizeDescription strips C0 controls + DEL from operator-authored
// descriptions before list returns them — keeps stray ESC or BEL from
// becoming terminal-injection in the AI client's UI. Tab and newline
// survive so multi-line descriptions still render.
func sanitizeDescription(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if r < 0x20 || r == 0x7F {
			return -1 // drop
		}
		return r
	}, s)
}

func joinForLog(path string, argv []string) string {
	if len(argv) == 0 {
		return logEscaper.Replace(path)
	}
	escaped := make([]string, len(argv))
	for i, a := range argv {
		escaped[i] = logEscaper.Replace(a)
	}
	return logEscaper.Replace(path) + " " + strings.Join(escaped, " ")
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

// safeUTF8 replaces invalid UTF-8 with U+FFFD so arbitrary command
// output (binary data, stray escapes) can be marshaled as JSON.
func safeUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	return strings.ToValidUTF8(string(b), "\uFFFD")
}

// resolveTarget returns the effective user a call should run as, or ""
// to deny. "self" in requested or in the allowed list resolves to the
// SSH user. A single-target rule implicitly elevates when the caller
// didn't ask for a different user (the common "always root" case).
func resolveTarget(requested string, allowed []string, self string) string {
	if requested == config.SelfUser {
		requested = self
	}
	var single string
	for _, u := range allowed {
		if u == config.SelfUser {
			u = self
		}
		if u == requested {
			return requested
		}
		single = u
	}
	if requested == self && len(allowed) == 1 {
		return single
	}
	return ""
}

func displayTarget(requested, self string) string {
	if requested == config.SelfUser {
		return self
	}
	return requested
}
