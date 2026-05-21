package server

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/binwiederhier/rrsh/config"
	"github.com/binwiederhier/rrsh/logger"
)

// testRule mirrors what config.convertRule produces: every entry is
// auto-anchored with ^(?:…)$. Defaults `As` to [self]; callers override
// after construction when they need elevation.
func testRule(command ...string) config.CommandRule {
	patterns := make([]*regexp.Regexp, len(command))
	for i, p := range command {
		patterns[i] = regexp.MustCompile("^(?:" + p + ")$")
	}
	return config.CommandRule{
		CommandPatterns: patterns,
		CommandSource:   append([]string(nil), command...),
		As:              []string{config.SelfUser},
	}
}

// testServer spins up a server.Server backed by a small default allowlist
// and feeds it the given NDJSON request stream. It returns the captured
// stdout (the server's response stream) and any transport-level error
// from Serve. Failures inside the helper itself are reported via t.
func testServer(t *testing.T, in string) (*bytes.Buffer, error) {
	t.Helper()
	cfg := &config.Config{
		Commands: []config.CommandRule{
			testRule("/bin/echo", ".*"),       // /bin/echo + one argv
			testRule("/bin/echo"),             // /bin/echo + zero argv
			testRule("/bin/echo", ".*", ".*"), // /bin/echo + two argv
			testRule("/bin/cat"),
			testRule("/bin/cat", ".*"),
			testRule("/bin/false"),
			testRule("/usr/bin/grep", ".*"),
		},
	}
	cfg.Commands[0].Description = "Echo one argument."
	cfg.Commands[3].Description = "Concatenate stdin."
	cfg.Commands[6].Description = "Filter lines."
	out := &bytes.Buffer{}
	srv := New(cfg, logger.New("tester"), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
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

// decodeRunResult unpacks `result` into a runResult, failing the test on
// any structural mismatch.
func decodeRunResult(t *testing.T, m map[string]any) runResult {
	t.Helper()
	resultObj, ok := m["result"]
	if !ok {
		t.Fatalf("response has no result: %v", m)
	}
	raw, err := json.Marshal(resultObj)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var rr runResult
	if err := json.Unmarshal(raw, &rr); err != nil {
		t.Fatalf("decode runResult: %v\nraw: %s", err, raw)
	}
	return rr
}

func TestServer_Hello(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Name:         "ntfy-prod-1",
		Instructions: "You are on the ntfy prod box. Call list first.",
		Commands:     []config.CommandRule{testRule("/bin/echo")},
	}
	Version = "v0.0.0-test"
	out := &bytes.Buffer{}
	in := `{"jsonrpc":"2.0","id":1,"method":"hello"}` + "\n"
	srv := New(cfg, logger.New("tester"), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeResponses(t, out.String())
	if len(resps) != 1 {
		t.Fatalf("got %d responses", len(resps))
	}
	result, ok := resps[0]["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got: %v", resps[0])
	}
	if result["name"] != cfg.Name {
		t.Errorf("name = %v, want %q", result["name"], cfg.Name)
	}
	if result["version"] != Version {
		t.Errorf("version = %v, want %q", result["version"], Version)
	}
	if result["instructions"] != cfg.Instructions {
		t.Errorf("instructions = %v, want %q", result["instructions"], cfg.Instructions)
	}
}

func TestServer_Hello_OmitsEmptyInstructions(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":1,"method":"hello"}` + "\n"
	out, _ := testServer(t, in)
	if strings.Contains(out.String(), `"instructions"`) {
		t.Errorf("instructions field should be omitted when empty, got: %s", out.String())
	}
}

func TestServer_Hello_DefaultName(t *testing.T) {
	t.Parallel()
	// Empty cfg.Name should fall back to "rrsh".
	in := `{"jsonrpc":"2.0","id":1,"method":"hello"}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	result := resps[0]["result"].(map[string]any)
	if result["name"] != "rrsh" {
		t.Errorf("name = %v, want %q", result["name"], "rrsh")
	}
}

func TestServer_List(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":2,"method":"list"}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	if len(resps) != 1 {
		t.Fatalf("got %d responses", len(resps))
	}
	result := resps[0]["result"].(map[string]any)
	commands := result["commands"].([]any)
	if len(commands) == 0 {
		t.Fatalf("expected at least one command")
	}
	// Spot-check: the first rule is /bin/echo + one ".*"
	first := commands[0].(map[string]any)
	cmd := first["command"].([]any)
	if cmd[0] != "/bin/echo" {
		t.Errorf("command[0] = %v, want /bin/echo", cmd[0])
	}
	if first["description"] != "Echo one argument." {
		t.Errorf("description = %v", first["description"])
	}
}

func TestServer_Run_Happy(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":4,"method":"run","params":{"argv":["/bin/echo","hello"]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	rr := decodeRunResult(t, resps[0])
	if rr.Exit != 0 {
		t.Errorf("exit = %d, want 0", rr.Exit)
	}
	if strings.TrimSpace(rr.Stdout) != "hello" {
		t.Errorf("stdout = %q", rr.Stdout)
	}
}

func TestServer_Run_Denied(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":5,"method":"run","params":{"argv":["/bin/rm","-rf","/"]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope, got: %v", resps[0])
	}
	if int(errObj["code"].(float64)) != errDenied {
		t.Errorf("code = %v, want %d", errObj["code"], errDenied)
	}
	if !strings.Contains(errObj["message"].(string), "/bin/rm") {
		t.Errorf("message should mention the command, got: %q", errObj["message"])
	}
}

func TestServer_Run_NonAbsolutePathDenied(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":6,"method":"run","params":{"argv":["echo","x"]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope for non-absolute path, got: %v", resps[0])
	}
	if int(errObj["code"].(float64)) != errDenied {
		t.Errorf("code = %v, want %d", errObj["code"], errDenied)
	}
}

func TestServer_Run_ArgWithSpacesIsLiteral(t *testing.T) {
	t.Parallel()
	// "hello world" stays one arg; /bin/echo prints it with the internal
	// space preserved.
	in := `{"jsonrpc":"2.0","id":7,"method":"run","params":{"argv":["/bin/echo","hello world"]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	rr := decodeRunResult(t, resps[0])
	if strings.TrimSpace(rr.Stdout) != "hello world" {
		t.Errorf("stdout = %q, want %q", rr.Stdout, "hello world")
	}
}

func TestServer_Run_Pipeline(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":8,"method":"run","params":{"pipeline":[{"argv":["/bin/echo","piped"]},{"argv":["/bin/cat"]}]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	rr := decodeRunResult(t, resps[0])
	if rr.Exit != 0 {
		t.Errorf("exit = %d", rr.Exit)
	}
	if strings.TrimSpace(rr.Stdout) != "piped" {
		t.Errorf("stdout = %q", rr.Stdout)
	}
}

func TestServer_Run_PipelineWithDeniedStage(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":9,"method":"run","params":{"pipeline":[{"argv":["/bin/echo","x"]},{"argv":["/bin/rm","-rf","/"]}]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope when a pipeline stage is denied, got: %v", resps[0])
	}
	if int(errObj["code"].(float64)) != errDenied {
		t.Errorf("code = %v, want %d", errObj["code"], errDenied)
	}
}

// Pipe character in an argument value must be passed through as a literal
// — this is the example the user gave that motivated the JSON-RPC route.
func TestServer_Run_PipeInsideArgIsLiteral(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":10,"method":"run","params":{"argv":["/bin/echo"," | > /dev/null"]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	rr := decodeRunResult(t, resps[0])
	if !strings.Contains(rr.Stdout, " | > /dev/null") {
		t.Errorf("stdout = %q — pipe/redirect bytes should survive as literal", rr.Stdout)
	}
}

func TestServer_Run_NonZeroExit(t *testing.T) {
	t.Parallel()
	// Child non-zero exit is NOT a transport error — it lives in result.exit.
	in := `{"jsonrpc":"2.0","id":11,"method":"run","params":{"argv":["/bin/false"]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	if _, isErr := resps[0]["error"]; isErr {
		t.Fatalf("non-zero child exit should not be an RPC error: %v", resps[0])
	}
	rr := decodeRunResult(t, resps[0])
	if rr.Exit == 0 {
		t.Errorf("exit = 0, want non-zero")
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

func TestServer_Notification_NoResponse(t *testing.T) {
	t.Parallel()
	// A request without an `id` field is a notification — no response is
	// emitted even if dispatch fails.
	in := `{"jsonrpc":"2.0","method":"no/such/method"}` + "\n"
	out, _ := testServer(t, in)
	if out.Len() != 0 {
		t.Errorf("expected no response for notification, got: %s", out.String())
	}
}

func TestServer_MultipleRequests(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":1,"method":"hello"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"list"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"run","params":{"argv":["/bin/echo","x"]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	if len(resps) != 3 {
		t.Fatalf("got %d responses, want 3", len(resps))
	}
	for i, r := range resps {
		gotID := int(r["id"].(float64))
		if gotID != i+1 {
			t.Errorf("response[%d] id = %d, want %d", i, gotID, i+1)
		}
	}
}

func TestServer_Run_BothArgvAndPipelineRejected(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":14,"method":"run","params":{"argv":["/bin/echo"],"pipeline":[{"argv":["/bin/echo"]}]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope, got: %v", resps[0])
	}
	if int(errObj["code"].(float64)) != errInvalidParams {
		t.Errorf("code = %v, want %d", errObj["code"], errInvalidParams)
	}
}

func TestServer_Run_OversizedRequestRejected(t *testing.T) {
	t.Parallel()
	// Build a request larger than maxRequestBytes — pad with whitespace
	// inside a string so the JSON is still well-formed if it were parsed.
	huge := strings.Repeat("x", maxRequestBytes+1024)
	in := `{"jsonrpc":"2.0","id":1,"method":"run","params":{"argv":["/bin/echo","` + huge + `"]}}` + "\n" +
		// Follow with a valid request to confirm we resync.
		`{"jsonrpc":"2.0","id":2,"method":"list"}` + "\n"
	out, err := testServer(t, in)
	if err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	resps := decodeResponses(t, out.String())
	if len(resps) != 2 {
		t.Fatalf("got %d responses, want 2: %s", len(resps), out.String())
	}
	if _, ok := resps[0]["error"].(map[string]any); !ok {
		t.Errorf("response 0 should be an error: %v", resps[0])
	}
	if _, ok := resps[1]["result"]; !ok {
		t.Errorf("response 1 should be a result: %v", resps[1])
	}
}

func TestServer_Run_ElevationDeniedWhenSudoDisabled(t *testing.T) {
	t.Parallel()
	echoRoot := testRule("/bin/echo", ".*")
	echoRoot.As = []string{"root"}
	echoRoot.Description = "Echo as root."
	cfg := &config.Config{
		Sudo:     false, // explicit, though it's the default
		Commands: []config.CommandRule{echoRoot},
	}
	out := &bytes.Buffer{}
	in := `{"jsonrpc":"2.0","id":1,"method":"run","params":{"argv":["/bin/echo","x"],"as":"root"}}` + "\n"
	srv := New(cfg, logger.New("tester"), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope when sudo:false and elevation requested, got: %v", resps[0])
	}
	if int(errObj["code"].(float64)) != errDenied {
		t.Errorf("code = %v, want %d", errObj["code"], errDenied)
	}
	if !strings.Contains(errObj["message"].(string), "sudo") {
		t.Errorf("error should mention sudo flag, got: %q", errObj["message"])
	}
}

func TestServer_Run_ElevationAllowedWhenSudoEnabled(t *testing.T) {
	t.Parallel()
	// We can't actually invoke /usr/bin/sudo in the test environment, but
	// we can verify the gate passes — the call should reach the executor
	// (which then fails to exec sudo, surfacing as a non-zero exit). The
	// point is: no "elevation disabled" RPC error.
	echoRoot := testRule("/bin/echo", ".*")
	echoRoot.As = []string{"root"}
	cfg := &config.Config{
		Sudo:     true,
		Commands: []config.CommandRule{echoRoot},
	}
	out := &bytes.Buffer{}
	in := `{"jsonrpc":"2.0","id":1,"method":"run","params":{"argv":["/bin/echo","x"],"as":"root"}}` + "\n"
	srv := New(cfg, logger.New("tester"), "tester", "/usr/bin/rrsh", strings.NewReader(in), out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	resps := decodeResponses(t, out.String())
	if errObj, isErr := resps[0]["error"].(map[string]any); isErr {
		if strings.Contains(errObj["message"].(string), "elevation disabled") {
			t.Errorf("should not say elevation disabled when sudo:true, got: %q", errObj["message"])
		}
	}
}

func TestServer_Run_PipelineLengthCapped(t *testing.T) {
	t.Parallel()
	stages := make([]string, maxPipelineStages+1)
	for i := range stages {
		stages[i] = `{"argv":["/bin/echo","x"]}`
	}
	in := `{"jsonrpc":"2.0","id":1,"method":"run","params":{"pipeline":[` + strings.Join(stages, ",") + `]}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	errObj, ok := resps[0]["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error envelope, got: %v", resps[0])
	}
	if !strings.Contains(errObj["message"].(string), "stage limit") {
		t.Errorf("error should mention stage limit, got: %q", errObj["message"])
	}
}

func TestServer_Run_Stdin(t *testing.T) {
	t.Parallel()
	in := `{"jsonrpc":"2.0","id":15,"method":"run","params":{"argv":["/bin/cat"],"stdin":"piped in"}}` + "\n"
	out, _ := testServer(t, in)
	resps := decodeResponses(t, out.String())
	rr := decodeRunResult(t, resps[0])
	if rr.Stdout != "piped in" {
		t.Errorf("stdout = %q, want %q", rr.Stdout, "piped in")
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
// cursor moves, BEL, BS) into AI-side rendering of list.
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
