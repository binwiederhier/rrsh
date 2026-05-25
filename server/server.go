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

	"github.com/binwiederhier/rrsh/auth"
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

// Server serves JSON-RPC requests over stdio.
type Server struct {
	cfg         *config.Config
	matcher     *matcher.Matcher
	log         *logger.SyslogLogger
	currentUser string // SSH user this server is running as
	rrsh        string // path to this binary for elevation re-exec
	in          *bufio.Reader
	out         io.Writer
}

// New constructs a Server. user is the SSH user (what "$USER" resolves
// to in `as:` lists). The binary path is taken from os.Executable() for
// the sudo re-exec.
func New(cfg *config.Config, log *logger.SyslogLogger, user string, in io.Reader, out io.Writer) (*Server, error) {
	rrsh, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve own executable path: %w", err)
	}
	return &Server{
		cfg:         cfg,
		matcher:     matcher.New(cfg.Commands),
		log:         log,
		currentUser: user,
		rrsh:        rrsh,
		in:          bufio.NewReaderSize(in, maxRequestBytes),
		out:         out,
	}, nil
}

// Serve runs the read/dispatch/write loop until stdin closes.
// Envelope-level errors come back as JSON-RPC error responses; only an
// irrecoverable stdin read error stops the loop.
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

// readLine reads one NDJSON line, capping at maxRequestBytes. On cap
// hit, tooLong is set and the rest of the line is drained so the next
// request reads cleanly.
func (s *Server) readLine() (line []byte, tooLong bool, err error) {
	for {
		fragment, fragErr := s.in.ReadSlice('\n')
		// ReadSlice returns the buffer's internal slice; append-copy
		// before the next read clobbers it.
		line = append(line, fragment...)
		if errors.Is(fragErr, bufio.ErrBufferFull) {
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

// discardToNewline drops bytes until '\n' or EOF, resyncing the
// framing after an oversized request was rejected.
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

// handle parses one request and returns a response (nil for
// notifications). Parse errors come back with a null ID.
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
	case "list_commands":
		return s.handleListCommands()
	case "run_command":
		return s.handleRunCommand(params)
	case "run_pipeline":
		return s.handleRunPipeline(params)
	default:
		return nil, &jsonrpcError{Code: errMethodNotFound, Message: "method not found: " + method}
	}
}

func (s *Server) handleListCommands() (any, *jsonrpcError) {
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
	return &listCommandsResult{
		Instructions: sanitizeDescription(s.cfg.Instructions),
		Commands:     out,
	}, nil
}

// handleRunCommand desugars a single argv into a one-stage pipeline.
// Top-level `as`/`stdin` map to the stage's `as` / the pipeline's stdin.
func (s *Server) handleRunCommand(params json.RawMessage) (any, *jsonrpcError) {
	if len(params) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_command requires params"}
	}
	var p runCommandParams
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "invalid run_command params: " + err.Error()}
	} else if len(p.Command) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_command requires non-empty command"}
	}
	return s.runPipeline([]runStep{{Command: p.Command, As: p.As}}, p.Stdin)
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

// runPipeline is the shared execution path for run_command (arrives
// as a one-stage pipeline) and run_pipeline. Matcher/auth denials
// surface as JSON-RPC errors (errDenied); child non-zero exits live
// in result.exit, not in the error envelope.
func (s *Server) runPipeline(steps []runStep, stdinStr string) (any, *jsonrpcError) {
	if len(steps) > maxPipelineStages {
		return nil, deny(fmt.Sprintf("pipeline exceeds %d-stage limit", maxPipelineStages))
	}
	stages := make([]*exec.Stage, 0, len(steps))
	for i, step := range steps {
		if len(step.Command) == 0 {
			return nil, &jsonrpcError{Code: errInvalidParams, Message: fmt.Sprintf("pipeline stage %d has empty command", i)}
		}
		origPath, origArgv := step.Command[0], step.Command[1:]
		asUser := normalizeUser(step.As, s.currentUser)

		// Build the exec form (sudo-wrapped if needed) before validation
		// so a denial log records what we would have run. The matcher
		// runs against the original below - rules describe user intent,
		// not the sudo plumbing.
		execPath, execArgv := origPath, origArgv
		if asUser != s.currentUser {
			execPath, execArgv = s.buildSudoCommand(asUser, origPath, origArgv)
		}
		var stdin io.Reader
		if i == 0 && stdinStr != "" {
			stdin = strings.NewReader(stdinStr)
		}
		stages = append(stages, &exec.Stage{
			Path:  execPath,
			Argv:  execArgv,
			Stdin: stdin,
		})

		rule, ok := s.matcher.Match(origPath, origArgv)
		if !ok {
			s.log.Denied(formatStagesForLog(stages), s.currentUser)
			return nil, deny("command not allowed: " + util.JoinForLog(origPath, origArgv))
		}
		if err := auth.Check(asUser, auth.Resolve(rule.As, s.currentUser)); err != nil {
			s.log.Denied(formatStagesForLog(stages), s.currentUser)
			return nil, deny(fmt.Sprintf("%s not permitted to run as %s", util.JoinForLog(origPath, origArgv), asUser))
		}
		stages[i].Rule = rule
	}
	s.log.Allowed(formatStagesForLog(stages), s.currentUser)
	return toRunResult(exec.ExecutePipeline(stages)), nil
}

// buildSudoCommand returns (path, argv) that spawns /usr/bin/sudo
// to re-enter rrsh's privileged half:
//
//	/usr/bin/sudo -n [-u USER] -- /usr/bin/rrsh sudo <path> <argv...>
//
// -u is omitted for root (sudo's default). The `--` is defense-in-depth:
// s.rrsh and path are both absolute today, but `--` protects against a
// future regression in either invariant.
func (s *Server) buildSudoCommand(user, path string, argv []string) (string, []string) {
	args := []string{"-n"}
	if user != "root" {
		args = append(args, "-u", user)
	}
	args = append(args, "--", s.rrsh, "sudo", path)
	args = append(args, argv...)
	return "/usr/bin/sudo", args
}
