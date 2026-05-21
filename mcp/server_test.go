package mcp

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/logger"
)

// testServer spins up an mcp.Server backed by a small default allowlist
// and feeds it the given NDJSON request stream. It returns the captured
// stdout (the server's response stream) and any transport-level error
// from Serve. Failures inside the helper itself are reported via t.
func testServer(t *testing.T, in string) (*bytes.Buffer, error) {
	t.Helper()
	cfg := &config.Config{
		Commands: []config.CommandRule{
			{Path: "/bin/echo", As: []string{config.SelfUser}, Description: "Echo arguments."},
			{Path: "/bin/cat", As: []string{config.SelfUser}, Description: "Concatenate input."},
			{Path: "/bin/false", As: []string{config.SelfUser}},
			{Path: "/usr/bin/grep", ArgsPatterns: []*regexp.Regexp{regexp.MustCompile(`^(?:.*)$`)}, As: []string{config.SelfUser}, Description: "Filter lines."},
		},
	}
	out := &bytes.Buffer{}
	// logger.New() opens syslog which may not be available in tests; we
	// tolerate nil writer, which the logger does. Just construct one.
	srv := New(cfg, logger.New(), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
	err := srv.Serve()
	return out, err
}

// decodeResponses parses a stream of NDJSON responses.
func decodeResponses(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	dec := json.NewDecoder(strings.NewReader(raw))
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode response: %v\nraw: %s", err, raw)
		}
		out = append(out, m)
	}
	return out
}

func TestServer_Initialize(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n"
	out, err := testServer(t, in)
	if err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	resps := decodeResponses(t, out.String())
	if len(resps) != 1 {
		t.Fatalf("got %d responses, want 1: %s", len(resps), out.String())
	}
	result, ok := resps[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got: %v", resps[0])
	}
	if result["protocolVersion"] != protocolVersion {
		t.Errorf("protocolVersion = %v, want %v", result["protocolVersion"], protocolVersion)
	}
}

func TestServer_ToolsList(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	if len(resps) != 1 {
		t.Fatalf("got %d responses", len(resps))
	}
	result := resps[0]["result"].(map[string]any)
	tools := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	if !names["list_commands"] || !names["run_command"] {
		t.Errorf("missing tools, got: %v", names)
	}
}

func TestServer_ListCommands(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_commands","arguments":{}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "/bin/echo") {
		t.Errorf("expected /bin/echo in allowlist, got: %s", text)
	}
	if !strings.Contains(text, "Echo arguments.") {
		t.Errorf("expected description in allowlist, got: %s", text)
	}
}

func TestServer_RunCommand_Happy(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","hello"]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); isErr {
		t.Fatalf("unexpected isError: %v", result)
	}
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var payload runCommandOutput
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("decode payload: %v\ntext: %s", err, text)
	}
	if payload.Exit != 0 {
		t.Errorf("exit = %d, want 0", payload.Exit)
	}
	if strings.TrimSpace(payload.Stdout) != "hello" {
		t.Errorf("stdout = %q", payload.Stdout)
	}
}

func TestServer_RunCommand_Denied(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/rm","-rf","/"]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for denied command, got: %v", result)
	}
}

func TestServer_RunCommand_NonAbsolutePathDenied(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["echo","x"]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for non-absolute path, got: %v", result)
	}
}

func TestServer_RunCommand_ArgWithSpacesIsLiteral(t *testing.T) {
	t.Parallel()
	// "hello world" stays one arg; /bin/echo prints it with the internal
	// space preserved.
	in := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","hello world"]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var payload runCommandOutput
	json.Unmarshal([]byte(text), &payload)
	if strings.TrimSpace(payload.Stdout) != "hello world" {
		t.Errorf("stdout = %q, want %q", payload.Stdout, "hello world")
	}
}

func TestServer_RunCommand_Pipeline(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"run_command","arguments":{"pipeline":[{"argv":["/bin/echo","piped"]},{"argv":["/bin/cat"]}]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var payload runCommandOutput
	json.Unmarshal([]byte(text), &payload)
	if payload.Exit != 0 {
		t.Errorf("exit = %d", payload.Exit)
	}
	if strings.TrimSpace(payload.Stdout) != "piped" {
		t.Errorf("stdout = %q", payload.Stdout)
	}
}

func TestServer_RunCommand_PipelineWithDeniedStage(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"run_command","arguments":{"pipeline":[{"argv":["/bin/echo","x"]},{"argv":["/bin/rm","-rf","/"]}]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true when a pipeline stage is denied")
	}
}

// Pipe character in an argument value must be passed through as a literal
// — this is the example the user gave that motivated the JSON-RPC route.
func TestServer_RunCommand_PipeInsideArgIsLiteral(t *testing.T) {
	t.Parallel()
	// argv containing pipe/redirect bytes must be dispatched without
	// rejection — the bytes are part of the string value, not shell
	// metacharacters. Use /bin/echo to confirm they survive the round-trip.
	in := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo"," | > /dev/null"]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var payload runCommandOutput
	json.Unmarshal([]byte(text), &payload)
	if !strings.Contains(payload.Stdout, " | > /dev/null") {
		t.Errorf("stdout = %q — pipe/redirect bytes should survive as literal", payload.Stdout)
	}
}

func TestServer_RunCommand_NonZeroExit(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/false"]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	// Non-zero exit surfaces isError=true.
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for /bin/false")
	}
}

func TestServer_MalformedJSON(t *testing.T) {
	t.Parallel()
	in := "this is not json\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	if len(resps) != 1 {
		t.Fatalf("got %d responses", len(resps))
	}
	if _, ok := resps[0]["error"].(map[string]any); !ok {
		t.Errorf("expected error object, got: %v", resps[0])
	}
}

func TestServer_UnknownMethod(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":12,"method":"no/such/method"}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got: %v", resps[0])
	}
	if int(errObj["code"].(float64)) != errMethodNotFound {
		t.Errorf("code = %v, want %d", errObj["code"], errMethodNotFound)
	}
}

func TestServer_UnknownTool(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	if _, ok := resps[0]["error"].(map[string]any); !ok {
		t.Errorf("expected error, got: %v", resps[0])
	}
}

func TestServer_Notification_NoResponse(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	out, _ := testServer(t, in)
	if out.Len() != 0 {
		t.Errorf("expected no response for notification, got: %s", out.String())
	}
}

func TestServer_MultipleRequests(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","x"]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}
	// IDs correlate.
	for i, r := range resps {
		gotID := int(r["id"].(float64))
		if gotID != i+1 {
			t.Errorf("response[%d] id = %d, want %d", i, gotID, i+1)
		}
	}
}

func TestServer_BothArgvAndPipelineRejected(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo"],"pipeline":[{"argv":["/bin/echo"]}]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true when both argv and pipeline set")
	}
}

func TestServer_Initialize_InstructionsAndName(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Name:         "ntfy-prod-1",
		Instructions: "You are on the ntfy prod box. Use list_commands to discover what is allowed.",
		Commands: []config.CommandRule{
			{Path: "/bin/echo", As: []string{config.SelfUser}},
		},
	}
	out := &bytes.Buffer{}
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n"
	srv := New(cfg, logger.New(), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if result["instructions"] != cfg.Instructions {
		t.Errorf("instructions = %v, want %q", result["instructions"], cfg.Instructions)
	}
	serverInfo := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != cfg.Name {
		t.Errorf("serverInfo.name = %v, want %q", serverInfo["name"], cfg.Name)
	}
}

func TestServer_Initialize_NoInstructionsEmitsNoField(t *testing.T) {
	t.Parallel()
	// Default cfg has no Instructions; the JSON response should omit the
	// field rather than emitting "instructions": "".
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n"
	out, _ := testServer(t, in)
	if strings.Contains(out.String(), `"instructions"`) {
		t.Errorf("instructions field should be omitted when empty, got: %s", out.String())
	}
}

func TestServer_RunCommand_OversizedRequestRejected(t *testing.T) {
	t.Parallel()
	// Build a request larger than maxRequestBytes — pad with whitespace
	// inside a string so the JSON is still well-formed if it were parsed.
	huge := strings.Repeat("x", maxRequestBytes+1024)
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","` + huge + `"]}}}` + "\n" +
		// Follow with a valid request to confirm we resync.
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	out, err := testServer(t, in)
	if err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	resps := decodeResponses(t, out.String())
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2: %s", len(resps), out.String())
	}
	// First response should be a parse error.
	if _, ok := resps[0]["error"].(map[string]any); !ok {
		t.Errorf("response 0 should be an error: %v", resps[0])
	}
	// Second response should succeed.
	if _, ok := resps[1]["result"]; !ok {
		t.Errorf("response 1 should be a result: %v", resps[1])
	}
}

func TestServer_RunCommand_ElevationDeniedWhenSudoDisabled(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Sudo: false, // explicit, though it's the default
		Commands: []config.CommandRule{
			{Path: "/bin/echo", As: []string{"root"}, Description: "Echo as root."},
		},
	}
	out := &bytes.Buffer{}
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","x"],"as":"root"}}}` + "\n"
	srv := New(cfg, logger.New(), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true when sudo:false and elevation requested, got: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "sudo") {
		t.Errorf("error message should mention sudo flag, got: %q", text)
	}
}

func TestServer_RunCommand_ElevationAllowedWhenSudoEnabled(t *testing.T) {
	t.Parallel()
	// We can't actually invoke /usr/bin/sudo in the test environment, but
	// we can verify the gate passes — the call should reach the executor
	// (which then fails to exec sudo, which surfaces as a non-zero exit).
	// The point is: no "elevation disabled" message.
	cfg := &config.Config{
		Sudo: true,
		Commands: []config.CommandRule{
			{Path: "/bin/echo", As: []string{"root"}},
		},
	}
	out := &bytes.Buffer{}
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","x"],"as":"root"}}}` + "\n"
	srv := New(cfg, logger.New(), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(text, "elevation disabled") {
		t.Errorf("should not say elevation disabled when sudo:true, got: %q", text)
	}
}

// TestJoinForLog_EscapesControlChars covers fix #2 — newlines, CRs and
// NUL bytes in argv elements must not be passed verbatim into syslog,
// or an authenticated caller could forge fake log records that look
// like legitimate ALLOWED/DENIED entries.
func TestJoinForLog_EscapesControlChars(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		path string
		argv []string
		want string
	}{
		{"newline", "/bin/echo", []string{"a\nALLOWED: root cmd=/bin/sh"}, "/bin/echo a\\nALLOWED: root cmd=/bin/sh"},
		{"cr", "/bin/echo", []string{"a\rb"}, "/bin/echo a\\rb"},
		{"nul", "/bin/echo", []string{"a\x00b"}, "/bin/echo a\\0b"},
		{"clean", "/bin/echo", []string{"hello", "world"}, "/bin/echo hello world"},
		{"path with newline", "/bin/x\n", nil, "/bin/x\\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := joinForLog(tc.path, tc.argv)
			if got != tc.want {
				t.Errorf("joinForLog = %q, want %q", got, tc.want)
			}
			if strings.ContainsAny(got, "\n\r\x00") {
				t.Errorf("result still contains raw control bytes: %q", got)
			}
		})
	}
}

// TestSanitizeDescription covers fix #8 — operator-authored description
// strings must not be able to inject terminal control sequences (ANSI
// cursor moves, BEL, BS) into AI-side rendering of list_commands.
func TestSanitizeDescription(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean", "Show username.", "Show username."},
		{"keeps newline", "line one\nline two", "line one\nline two"},
		{"keeps tab", "col1\tcol2", "col1\tcol2"},
		{"strips ESC", "before\x1b[2Jafter", "before[2Jafter"},
		{"strips BEL", "ding\x07ding", "dingding"},
		{"strips BS", "rewrite\x08\x08\x08foo", "rewritefoo"},
		{"strips NUL", "a\x00b", "ab"},
		{"strips DEL", "a\x7fb", "ab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeDescription(tc.in)
			if got != tc.want {
				t.Errorf("sanitizeDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestServer_RunCommand_PipelineLengthCapped covers fix #3 — a pipeline
// with more than maxPipelineStages stages must be rejected before any
// process is spawned.
func TestServer_RunCommand_PipelineLengthCapped(t *testing.T) {
	t.Parallel()
	// Build a pipeline of maxPipelineStages+1 trivial echo stages.
	stages := make([]string, maxPipelineStages+1)
	for i := range stages {
		stages[i] = `{"argv":["/bin/echo","x"]}`
	}
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_command","arguments":{"pipeline":[` + strings.Join(stages, ",") + `]}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for over-cap pipeline, got: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "stage limit") {
		t.Errorf("error message should mention stage limit, got: %q", text)
	}
}

func TestServer_RunCommand_Stdin(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/cat"],"stdin":"piped in"}}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	var payload runCommandOutput
	json.Unmarshal([]byte(text), &payload)
	if payload.Stdout != "piped in" {
		t.Errorf("stdout = %q, want %q", payload.Stdout, "piped in")
	}
}
