package mcp

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/logger"
)

func testServer(in string) (*bytes.Buffer, error) {
	cfg := &config.Config{
		Timeout: 5 * time.Second,
		Commands: []config.CommandRule{
			{Path: "/bin/echo", As: []string{config.SelfUser}, Description: "Echo arguments."},
			{Path: "/bin/cat", As: []string{config.SelfUser}, Description: "Concatenate input."},
			{Path: "/bin/false", As: []string{config.SelfUser}},
			{Path: "/usr/bin/grep", ArgsPattern: regexp.MustCompile(`.*`), As: []string{config.SelfUser}, Description: "Filter lines."},
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
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n"
	out, err := testServer(in)
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
	if result["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %v", result["protocolVersion"], ProtocolVersion)
	}
}

func TestServer_ToolsList(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	out, _ := testServer(in)
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
	in := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_commands","arguments":{}}}` + "\n"
	out, _ := testServer(in)
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
	in := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","hello"]}}}` + "\n"
	out, _ := testServer(in)
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
	in := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/rm","-rf","/"]}}}` + "\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for denied command, got: %v", result)
	}
}

func TestServer_RunCommand_NonAbsolutePathDenied(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["echo","x"]}}}` + "\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for non-absolute path, got: %v", result)
	}
}

func TestServer_RunCommand_ArgWithSpacesIsLiteral(t *testing.T) {
	// "hello world" stays one arg; /bin/echo prints it with the internal
	// space preserved.
	in := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","hello world"]}}}` + "\n"
	out, _ := testServer(in)
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
	in := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"run_command","arguments":{"pipeline":[{"argv":["/bin/echo","piped"]},{"argv":["/bin/cat"]}]}}}` + "\n"
	out, _ := testServer(in)
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
	in := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"run_command","arguments":{"pipeline":[{"argv":["/bin/echo","x"]},{"argv":["/bin/rm","-rf","/"]}]}}}` + "\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true when a pipeline stage is denied")
	}
}

// Pipe character in an argument value must be passed through as a literal
// — this is the example the user gave that motivated the JSON-RPC route.
func TestServer_RunCommand_PipeInsideArgIsLiteral(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/usr/bin/grep","--help"]}}}` + "\n"
	// We can't easily exercise grep's behavior without a fixture, but we
	// can confirm an argv containing pipe/redirect bytes is matched and
	// dispatched without rejection. Use /bin/echo to inspect the result.
	in = `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo"," | > /dev/null"]}}}` + "\n"
	out, _ := testServer(in)
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
	in := `{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/false"]}}}` + "\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	// Non-zero exit surfaces isError=true.
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true for /bin/false")
	}
}

func TestServer_MalformedJSON(t *testing.T) {
	in := "this is not json\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	if len(resps) != 1 {
		t.Fatalf("got %d responses", len(resps))
	}
	if _, ok := resps[0]["error"].(map[string]any); !ok {
		t.Errorf("expected error object, got: %v", resps[0])
	}
}

func TestServer_UnknownMethod(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":12,"method":"no/such/method"}` + "\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error, got: %v", resps[0])
	}
	if int(errObj["code"].(float64)) != ErrMethodNotFound {
		t.Errorf("code = %v, want %d", errObj["code"], ErrMethodNotFound)
	}
}

func TestServer_UnknownTool(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"no_such_tool","arguments":{}}}` + "\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	if _, ok := resps[0]["error"].(map[string]any); !ok {
		t.Errorf("expected error, got: %v", resps[0])
	}
}

func TestServer_Notification_NoResponse(t *testing.T) {
	in := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	out, _ := testServer(in)
	if out.Len() != 0 {
		t.Errorf("expected no response for notification, got: %s", out.String())
	}
}

func TestServer_MultipleRequests(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo","x"]}}}` + "\n"
	out, _ := testServer(in)
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
	in := `{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/echo"],"pipeline":[{"argv":["/bin/echo"]}]}}}` + "\n"
	out, _ := testServer(in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("expected isError=true when both argv and pipeline set")
	}
}

func TestServer_RunCommand_Stdin(t *testing.T) {
	in := `{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"run_command","arguments":{"argv":["/bin/cat"],"stdin":"piped in"}}}` + "\n"
	out, _ := testServer(in)
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
