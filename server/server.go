// Package server implements the rrsh JSON-RPC 2.0 server over stdio.
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

	"github.com/binwiederhier/rrsh/audit"
	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/exec"
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
	log         *audit.Logger
	currentUser string // SSH user this server is running as
	selfPath    string // path to this binary for elevation re-exec
	in          *bufio.Reader
	out         io.Writer
}

// New constructs a Server. user is the SSH user (the default "as:"
// target when a request doesn't specify one). The binary path is
// taken from os.Executable() for the sudo re-exec.
func New(cfg *config.Config, log *audit.Logger, user string, in io.Reader, out io.Writer) (*Server, error) {
	selfPath, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot resolve own executable path: %w", err)
	}
	return &Server{
		cfg:         cfg,
		matcher:     matcher.New(cfg.Commands, user),
		log:         log,
		currentUser: user,
		selfPath:    selfPath,
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

// dispatch routes a parsed request to its handler. The concrete result
// types are wrapped in `any` here so each handler can keep its own
// typed return.
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

func (s *Server) handleListCommands() (*listCommandsResult, *jsonrpcError) {
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

// handleRunCommand desugars a single command into a one-stage pipeline.
// Top-level `as`/`stdin` map to the stage's `as` / the pipeline's stdin.
func (s *Server) handleRunCommand(params json.RawMessage) (*runResult, *jsonrpcError) {
	var p runCommandParams
	if err := decodeParams("run_command", params, &p); err != nil {
		return nil, err
	}
	if len(p.Command) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_command requires non-empty command"}
	}
	return s.runPipeline([]runStep{{Command: p.Command, As: p.As}}, p.Stdin)
}

// handleRunPipeline executes a multi-stage pipeline. `stdin` (if set)
// feeds stage 0; each stage's `as` is independent.
func (s *Server) handleRunPipeline(params json.RawMessage) (*runResult, *jsonrpcError) {
	var p runPipelineParams
	if err := decodeParams("run_pipeline", params, &p); err != nil {
		return nil, err
	}
	if len(p.Pipeline) == 0 {
		return nil, &jsonrpcError{Code: errInvalidParams, Message: "run_pipeline requires non-empty pipeline"}
	}
	return s.runPipeline(p.Pipeline, p.Stdin)
}

// runPipeline is the shared execution path for run_command (arrives
// as a one-stage pipeline) and run_pipeline. Matcher denials surface
// as JSON-RPC errors (errDenied); child non-zero exits live in
// result.exit, not in the error envelope.
func (s *Server) runPipeline(steps []runStep, stdinStr string) (*runResult, *jsonrpcError) {
	if len(steps) > maxPipelineStages {
		return nil, deny(fmt.Sprintf("pipeline exceeds %d-stage limit", maxPipelineStages))
	}
	stages := make([]*exec.Stage, 0, len(steps))
	for i, step := range steps {
		if len(step.Command) == 0 {
			return nil, &jsonrpcError{Code: errInvalidParams, Message: fmt.Sprintf("pipeline stage %d has empty command", i)}
		}
		stage, err := s.buildStage(step, i == 0, stdinStr)
		if err != nil {
			return nil, err
		}
		stages = append(stages, stage)
	}
	s.log.Allowed(formatStagesForLog(stages), s.currentUser)
	return toRunResult(exec.ExecutePipeline(stages)), nil
}

// buildStage validates one step against the matcher and produces the
// executor Stage. On denial it writes the audit log and returns an
// errDenied jsonrpcError.
func (s *Server) buildStage(step runStep, isFirst bool, stdinStr string) (*exec.Stage, *jsonrpcError) {
	rule, ok := s.matcher.MatchAsUser(step.Command, step.As)
	if !ok {
		s.log.Denied(util.JoinForLog(step.Command), s.currentUser)
		return nil, deny("command not allowed: " + util.JoinForLog(step.Command))
	}
	command := step.Command
	if step.As != "" && step.As != s.currentUser {
		command = s.buildSudoCommand(step.As, step.Command)
	}
	var stdin io.Reader
	if isFirst && stdinStr != "" {
		stdin = strings.NewReader(stdinStr)
	}
	return &exec.Stage{
		Command: command,
		Timeout: rule.Timeout,
		Stdin:   stdin,
	}, nil
}

// buildSudoCommand returns the full command (path + args) that spawns
// /usr/bin/sudo to re-enter rrsh's privileged half:
//
//	/usr/bin/sudo -n [-u USER] -- /usr/bin/rrsh sudo <command...>
//
// -u is omitted for root (sudo's default). The `--` is defense-in-depth:
// selfPath and command[0] are both absolute today, but `--` protects
// against a future regression in either invariant.
func (s *Server) buildSudoCommand(user string, command []string) []string {
	wrapped := []string{"/usr/bin/sudo", "-n"}
	if user != "root" {
		wrapped = append(wrapped, "-u", user)
	}
	wrapped = append(wrapped, "--", s.selfPath, "sudo")
	wrapped = append(wrapped, command...)
	return wrapped
}
