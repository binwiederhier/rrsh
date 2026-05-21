package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/exec"
	"github.com/binwiederhier/rrsh/logger"
	"github.com/binwiederhier/rrsh/matcher"
)

const (
	maxRequestBytes   = 1 << 20 // 1 MiB
	maxPipelineStages = 16
)

// Server holds the dependencies needed to serve JSON-RPC requests.
type Server struct {
	cfg     *config.Config
	matcher *matcher.Matcher
	log     *logger.SyslogLogger
	user    string // current SSH user
	rrsh    string // path to this binary for elevation re-exec
	in      *bufio.Reader
	out     io.Writer
}

// New constructs a Server. `self` is the current SSH user (what "self"
// resolves to in `as:` lists). The path to the running binary is
// resolved internally via os.Executable() and used to re-exec under
// sudo when a call needs elevation.
func New(cfg *config.Config, log *logger.SyslogLogger, user string, in io.Reader, out io.Writer) (*Server, error) {
	rrsh, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve own executable path: %w", err)
	}
	return &Server{
		cfg:     cfg,
		matcher: matcher.New(cfg.Commands),
		log:     log,
		user:    user,
		rrsh:    rrsh,
		in:      bufio.NewReaderSize(in, 1<<20),
		out:     out,
	}, nil
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
	out := make([]*commandEntry, 0, len(s.cfg.Commands))
	for _, r := range s.cfg.Commands {
		entry := &commandEntry{
			Command:     r.CommandSource,
			As:          r.As,
			Description: sanitizeDescription(r.Description),
		}
		if r.Timeout > 0 {
			entry.TimeoutSecs = r.Timeout.Seconds()
		}
		out = append(out, entry)
	}
	return &helloResult{
		Name:         name,
		Instructions: s.cfg.Instructions,
		Commands:     out,
	}, nil
}

// handleRun requires exactly one of `argv` or `pipeline`. Matcher
// denials surface as JSON-RPC errors (code errDenied) so the AI client
// gets a clean signal - the child's own non-zero exit lives in
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
		return s.runCommand(p)
	}
	return s.runPipeline(p)
}

// runCommand executes a single-argv run call.
func (s *Server) runCommand(p runParams) (any, *rpcError) {
	path := p.Argv[0]
	argv := p.Argv[1:]

	rule, ok := s.matcher.Match(path, argv)
	if !ok {
		input := joinForLog(path, argv)
		s.log.Denied(input, s.user)
		return nil, deny("command not allowed: " + input)
	}

	requested := p.As
	if requested == "" {
		requested = config.SelfUser
	}
	user := resolveUser(requested, rule.As, s.user)
	if user == "" {
		input := joinForLog(path, argv)
		s.log.Denied(input, s.user)
		return nil, deny(fmt.Sprintf("%s not permitted to run as %s", input, displayUser(requested, s.user)))
	}

	var stdin io.Reader
	if p.Stdin != "" {
		stdin = strings.NewReader(p.Stdin)
	}

	input := joinForLog(path, argv)
	if user == s.user {
		s.log.Allowed(input, s.user)
		return toRunResult(exec.Execute(path, argv, rule, stdin)), nil
	}

	if !s.cfg.Sudo {
		s.log.Denied(input, s.user)
		return nil, deny("elevation disabled in config (set \"sudo\": true in /etc/rrsh/rrsh.json)")
	}

	s.log.Allowed(input, user)
	return toRunResult(s.elevateAndExecute(user, path, argv, rule, stdin)), nil
}

// runPipeline executes a multi-stage pipeline.
func (s *Server) runPipeline(p runParams) (any, *rpcError) {
	if p.As != "" {
		return nil, &rpcError{Code: errInvalidParams, Message: "top-level `as` is not valid with pipeline - set `as` per stage"}
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
			s.log.Denied(s.fullPipelineLog(p.Pipeline), s.user)
			return nil, deny("pipeline stage not allowed: " + input)
		}

		requested := step.As
		if requested == "" {
			requested = config.SelfUser
		}
		user := resolveUser(requested, rule.As, s.user)
		if user == "" {
			input := joinForLog(path, argv)
			s.log.Denied(s.fullPipelineLog(p.Pipeline), s.user)
			return nil, deny(fmt.Sprintf("pipeline stage %s not permitted to run as %s", input, displayUser(requested, s.user)))
		}

		// For elevation in a pipeline, rewrite the stage to invoke
		// /usr/bin/sudo … rrsh sudo <path> <argv>. The privileged half
		// re-validates from disk against the same rule's `as:` list.
		if user != s.user {
			if !s.cfg.Sudo {
				s.log.Denied(s.fullPipelineLog(p.Pipeline), s.user)
				return nil, deny("pipeline stage requires elevation but sudo is disabled in config (set \"sudo\": true)")
			}
			path, argv = s.buildElevatedArgv(user, path, argv)
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

	s.log.Allowed(s.fullPipelineLog(p.Pipeline), s.user)
	return toRunResult(exec.ExecutePipeline(stages)), nil
}

// elevateAndExecute re-execs the rrsh binary via /usr/bin/sudo to run the
// command as `user`. The privileged half (cmd/sudo.go) reads its argv
// directly from os.Args, re-loads config from disk, and re-validates the
// rule's `as:` list before executing.
func (s *Server) elevateAndExecute(user, path string, argv []string, rule *config.CommandRule, stdin io.Reader) *exec.Result {
	sudoPath, sudoArgv := s.buildElevatedArgv(user, path, argv)
	// Pretend the elevation is just another command - same executor
	// semantics, same timeout.
	return exec.Execute(sudoPath, sudoArgv, rule, stdin)
}

// buildElevatedArgv produces (path, argv) suitable for exec.Execute to
// spawn /usr/bin/sudo and re-enter rrsh's privileged half.
//
//	sudo -n [-u USER] /usr/bin/rrsh sudo <path> <argv...>
//
// -u is omitted when user == "root" (sudo defaults to root).
func (s *Server) buildElevatedArgv(user, path string, argv []string) (string, []string) {
	out := []string{"-n"}
	if user != "root" {
		out = append(out, "-u", user)
	}
	out = append(out, s.rrsh, "sudo", path)
	out = append(out, argv...)
	return "/usr/bin/sudo", out
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
