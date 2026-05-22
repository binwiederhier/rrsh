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
	"github.com/binwiederhier/rrsh/util"
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
	user    string // current user
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
		in:      bufio.NewReaderSize(in, maxRequestBytes),
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
		eof := errors.Is(err, io.EOF)
		if err != nil && !eof {
			return err
		} else if eof && len(line) == 0 {
			return nil
		}
		if tooLong {
			tooLongResp := errResponse(nil, errParse, fmt.Sprintf("request exceeds %d-byte limit", maxRequestBytes))
			if err := enc.Encode(tooLongResp); err != nil {
				return err
			}
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			if eof {
				return nil
			}
			continue
		}
		if resp := s.handle(line); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
		if eof {
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
func (s *Server) handle(data []byte) *jsonrpcResponse {
	var req jsonrpcRequest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return errResponse(nil, errParse, "parse error: "+err.Error())
	} else if req.JSONRPC != "2.0" {
		return errResponse(req.ID, errInvalidRequest, "jsonrpc must be \"2.0\"")
	} else if req.Method == "" {
		return errResponse(req.ID, errInvalidRequest, "method is required")
	}
	// Notifications have no ID and never get a reply.
	isNotification := len(req.ID) == 0
	result, rpcErr := s.dispatch(req.Method, req.Params)
	if isNotification {
		return nil
	} else if rpcErr != nil {
		return &jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}
	}
	return &jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
}

// dispatch routes a parsed request to its handler.
func (s *Server) dispatch(method string, params json.RawMessage) (any, *jsonrpcError) {
	switch method {
	case "hello":
		return s.handleHello()
	case "run_command":
		return s.handleRunCommand(params)
	case "run_pipeline":
		return s.handleRunPipeline(params)
	default:
		return nil, &jsonrpcError{Code: errMethodNotFound, Message: "method not found: " + method}
	}
}

func (s *Server) handleHello() (any, *jsonrpcError) {
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
		Instructions: s.cfg.Instructions,
		Commands:     out,
	}, nil
}

// handleRunCommand wraps a single argv into a one-stage pipeline and
// delegates to runPipeline. Top-level `as` and `stdin` map to the
// stage's `as` and the pipeline's `stdin`.
func (s *Server) handleRunCommand(params json.RawMessage) (any, *jsonrpcError) {
	if len(params) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_command requires params"}
	}
	var p runCommandParams
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "invalid run_command params: " + err.Error()}
	} else if len(p.Argv) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_command requires non-empty argv"}
	}
	return s.runPipeline([]*runStep{{Argv: p.Argv, As: p.As}}, p.Stdin)
}

// handleRunPipeline executes a multi-stage pipeline. `stdin` (if set)
// feeds stage 0; each stage's `as` is independent.
func (s *Server) handleRunPipeline(params json.RawMessage) (any, *jsonrpcError) {
	if len(params) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_pipeline requires params"}
	}
	var p runPipelineParams
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "invalid run_pipeline params: " + err.Error()}
	}
	if len(p.Pipeline) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_pipeline requires non-empty pipeline"}
	}
	return s.runPipeline(p.Pipeline, p.Stdin)
}

// runPipeline is the single execution path for both run_command (which
// arrives as a one-stage pipeline) and run_pipeline. A one-stage call
// logs and behaves identically to a direct argv call - the syslog
// entry has no " | " separator because there is only one stage. Matcher
// denials surface as JSON-RPC errors (code errDenied) so the AI client
// gets a clean signal - the child's own non-zero exit lives in
// result.exit, not in the RPC error envelope.
func (s *Server) runPipeline(steps []*runStep, stdinStr string) (any, *jsonrpcError) {
	if len(steps) > maxPipelineStages {
		return nil, deny(fmt.Sprintf("pipeline exceeds %d-stage limit", maxPipelineStages))
	}
	commandForLog := formatCommandForLog(steps)
	stages := make([]*exec.Stage, 0, len(steps))
	for i, step := range steps {
		if len(step.Argv) == 0 {
			return nil, &jsonrpcError{Code: errInvalidParams, Message: fmt.Sprintf("pipeline stage %d has empty argv", i)}
		}
		path := step.Argv[0]
		argv := step.Argv[1:]

		// Make sure command is allowed
		requestedUser := normalizeUser(step.As, s.user)
		rule, ok := s.matcher.Match(path, argv)
		if !ok {
			s.log.Denied(commandForLog, requestedUser)
			return nil, deny("command not allowed: " + util.JoinForLog(path, argv))
		}

		// Make sure the requested user is allowed to run the command
		if err := authorizeUser(requestedUser, s.user, rule.As); err != nil {
			s.log.Denied(commandForLog, requestedUser)
			return nil, deny(fmt.Sprintf("%s not permitted to run as %s", util.JoinForLog(path, argv), requestedUser))
		}

		// Maybe re-write command as: /usr/bin/sudo rrsh sudo <path> <argv>.
		// If /etc/sudoers.d/rrsh isn't uncommented, the spawned sudo will
		// fail with a clear "not allowed to execute" stderr that surfaces
		// in result.stderr - no separate gate at this layer.
		if requestedUser != s.user {
			path, argv = s.buildSudoCommand(requestedUser, path, argv)
		}

		var stdin io.Reader
		if i == 0 && stdinStr != "" {
			stdin = strings.NewReader(stdinStr)
		}
		stages = append(stages, &exec.Stage{
			Path:  path,
			Argv:  argv,
			Rule:  rule,
			Stdin: stdin,
		})
	}
	s.log.Allowed(commandForLog, s.user)
	return toRunResult(exec.ExecutePipeline(stages)), nil
}

// buildSudoCommand produces (path, argv) suitable for exec.Execute to
// spawn /usr/bin/sudo and re-enter rrsh's privileged half.
//
//	/usr/bin/sudo --non-interactive [--user=USER] /usr/bin/rrsh sudo <path> <argv...>
//
// -u is omitted when user == "root" (sudo defaults to root).
func (s *Server) buildSudoCommand(user, path string, argv []string) (string, []string) {
	args := []string{"--non-interactive"}
	if user != "root" {
		args = append(args, "--user="+user)
	}
	args = append(args, s.rrsh, "sudo", path)
	args = append(args, argv...)
	return "/usr/bin/sudo", args
}
