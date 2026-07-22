package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestChooseSessionFileThenDestination_ShareOneReader is a regression test
// for a live bug: chooseSessionFile and parseDestinationChoice each used to
// construct their own bufio.NewReader(os.Stdin). bufio.Reader reads ahead
// into its own internal buffer, so when chooseSessionFile's reader pulled
// both queued answers ("1\n" and "2\n") from stdin in one underlying read,
// only "1\n" was returned to the caller — "2\n" stayed trapped in that
// first reader's buffer. A second, independent reader constructed later by
// parseDestinationChoice then saw stdin as already drained and failed with
// "invalid choice \"\"" instead of reading "2". The fix is to construct
// exactly one *bufio.Reader per run and pass it to both call sites; this
// test proves sequential reads off a single shared reader work correctly.
func TestChooseSessionFileThenDestination_ShareOneReader(t *testing.T) {
	dir := t.TempDir()
	files := make([]sessionFile, 2)
	for i := range files {
		path := filepath.Join(dir, "session"+string(rune('A'+i))+".jsonl")
		line := `{"type":"user","isSidechain":false,"isMeta":false,"timestamp":"2026-07-19T10:00:00Z","message":{"role":"user","content":"hello from session"}}` + "\n"
		if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
			t.Fatalf("could not write fixture session file: %v", err)
		}
		files[i] = sessionFile{Path: path, ModTime: int64(i)}
	}

	// One shared reader, fed both answers up front — exactly as os.Stdin
	// would be when the user pipes "1\n2\n" into the process.
	reader := bufio.NewReader(strings.NewReader("1\n2\n"))

	chosen, err := chooseSessionFile(files, reader)
	if err != nil {
		t.Fatalf("chooseSessionFile returned error: %v", err)
	}
	if chosen.Path != files[0].Path {
		t.Fatalf("expected first session choice %q, got %q", files[0].Path, chosen.Path)
	}

	destination, err := parseDestinationChoice(reader)
	if err != nil {
		t.Fatalf("parseDestinationChoice returned error: %v", err)
	}
	if destination != "opencode" {
		t.Fatalf("expected second read off the shared reader to yield %q, got %q", "opencode", destination)
	}
}

func TestChooseDestination_ParsesEachValidChoice(t *testing.T) {
	cases := map[string]string{
		"1\n": "web",
		"2\n": "opencode",
		"3\n": "codex",
		"4\n": "antigravity",
	}
	for input, want := range cases {
		got, err := parseDestinationChoice(bufio.NewReader(strings.NewReader(input)))
		if err != nil {
			t.Fatalf("input %q: unexpected error: %v", input, err)
		}
		if got != want {
			t.Errorf("input %q: got %q, want %q", input, got, want)
		}
	}
}

func TestChooseDestination_RejectsInvalidChoice(t *testing.T) {
	_, err := parseDestinationChoice(bufio.NewReader(strings.NewReader("9\n")))
	if err == nil {
		t.Fatal("expected an error for an out-of-range choice, got nil")
	}
}

func TestPostLocalHandoffCredit_SendsToolAndKey(t *testing.T) {
	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body := map[string]string{}
		json.NewDecoder(r.Body).Decode(&body)
		gotBody = body["tool"]
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	t.Setenv("SHARETOAI_API_URL", server.URL)

	err := postLocalHandoffCredit("test-key", "opencode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("expected Authorization header, got %q", gotAuth)
	}
	if gotBody != "opencode" {
		t.Errorf("expected tool=opencode in request body, got %q", gotBody)
	}
}

// TestDispatchLocalDestination_OpenCode is a regression test for the actual
// switch inside runPush() that maps a destination choice to the correct
// writer: it asserts destination "opencode" writes a real, importable
// OpenCode session file and reports the right auto-run command.
func TestDispatchLocalDestination_OpenCode(t *testing.T) {
	messages := []parsedMessage{
		{Role: "user", Content: "how do I deploy this?", Timestamp: "2026-07-19T10:00:00Z"},
	}
	cwd := t.TempDir()

	path, runBin, runArgs, hint, err := dispatchLocalDestination("opencode", messages, cwd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	if !strings.HasSuffix(path, ".json") {
		t.Errorf("expected a .json file, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	var out openCodeSessionExport
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("written file isn't valid OpenCode export JSON: %v", err)
	}
	if len(out.Messages) != 1 || out.Messages[0].Parts[0].Text != "how do I deploy this?" {
		t.Errorf("expected message content to round-trip into the OpenCode file, got %+v", out.Messages)
	}

	if runBin != "opencode" {
		t.Errorf("expected runBin %q, got %q", "opencode", runBin)
	}
	if len(runArgs) != 2 || runArgs[0] != "import" || runArgs[1] != path {
		t.Errorf("expected runArgs [import %s], got %v", path, runArgs)
	}
	if !strings.Contains(hint, path) {
		t.Errorf("expected hint to mention the written path %q, got %q", path, hint)
	}
}

// TestDispatchLocalDestination_Codex is the same regression test for
// destination "codex": it must write a real rollout-*.jsonl file under
// ~/.codex/sessions and report the "codex resume --last" auto-run command.
func TestDispatchLocalDestination_Codex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir() reads this
	t.Setenv("HOME", home)        // macOS/Linux

	messages := []parsedMessage{
		{Role: "user", Content: "explain this function", Timestamp: "2026-07-19T10:00:00Z"},
	}

	path, runBin, runArgs, hint, err := dispatchLocalDestination("codex", messages, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.HasPrefix(path, home) {
		t.Errorf("expected path under home dir %q, got %q", home, path)
	}
	if !strings.HasSuffix(path, ".jsonl") {
		t.Errorf("expected a .jsonl file, got %q", path)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Errorf("expected the Codex rollout file to actually exist on disk: %v", statErr)
	}

	if runBin != "codex" {
		t.Errorf("expected runBin %q, got %q", "codex", runBin)
	}
	if len(runArgs) != 2 || runArgs[0] != "resume" || runArgs[1] != "--last" {
		t.Errorf("expected runArgs [resume --last], got %v", runArgs)
	}
	if !strings.Contains(hint, "codex resume --last") {
		t.Errorf("expected hint to mention the resume command, got %q", hint)
	}
}

// TestDispatchLocalDestination_Antigravity is the regression test for
// destination "antigravity": it must write a readable Markdown file and
// report that there's nothing to auto-run (Antigravity has no import CLI).
//
// A real SQLite-based conversation-injection path was built, shipped, and
// live-tested successfully once — then found to be unreliable: Antigravity
// CLI's own /resume documentation confirms `--conversation` queries
// Google's backend to verify a trajectory before loading it, so a
// locally-injected conversation (never registered with that backend) fails
// deterministically. Confirmed via repeated live testing against a real
// install, and independently corroborated by a separate open-source
// cross-tool handoff project's own notes on Antigravity needing "optional
// live RPC." Reverted to the plain-Markdown-paste fallback, which is
// reliable.
func TestDispatchLocalDestination_Antigravity(t *testing.T) {
	messages := []parsedMessage{
		{Role: "user", Content: "what does this error mean?", Timestamp: "2026-07-19T10:00:00Z"},
	}

	path, runBin, runArgs, hint, err := dispatchLocalDestination("antigravity", messages, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	if !strings.HasSuffix(path, ".md") {
		t.Errorf("expected a .md file, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if !strings.Contains(string(data), "what does this error mean?") {
		t.Errorf("expected message content in written file, got: %s", data)
	}

	if runBin != "" {
		t.Errorf("Antigravity has nothing to auto-run, expected empty runBin, got %q", runBin)
	}
	if runArgs != nil {
		t.Errorf("expected nil runArgs for Antigravity, got %v", runArgs)
	}
	if hint == "" {
		t.Error("expected a non-empty hint explaining the manual paste step")
	}
}

func TestDispatchLocalDestination_UnknownDestinationErrors(t *testing.T) {
	_, _, _, _, err := dispatchLocalDestination("carrier-pigeon", nil, t.TempDir())
	if err == nil {
		t.Fatal("expected an error for an unrecognized destination, got nil")
	}
}

// TestFirstUserMessageSnippet_SkipsSlashCommandEnvelopes is a regression
// test for a real bug: sessions whose first turn is a slash command (e.g.
// the user ran `/login` before typing anything else) showed the raw
// "<command-name>/login</command-name> ..." XML-ish envelope as the
// picker's label instead of a readable snippet. Claude Code stores slash-
// command invocations as literal role="user" messages with this envelope
// as their content — verified against real session files on this machine,
// which also showed the same pattern for "<command-message>",
// "<local-command-stdout>", and "<task-notification>" wrapped content.
// sessionLabel must skip these and use the first genuine free-text user
// turn instead, when there's no ai-title line to prefer (see
// TestSessionLabel_PrefersClaudeCodesOwnAITitle below).
func TestSessionLabel_SkipsSlashCommandEnvelopes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"user","isSidechain":false,"isMeta":false,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"<command-name>/login</command-name>\n            <command-message>login</command-message>\n            <command-args></command-args>"}}`,
		`{"type":"user","isSidechain":false,"isMeta":false,"timestamp":"2026-07-20T10:00:01Z","message":{"role":"user","content":"<local-command-stdout>Login successful</local-command-stdout>"}}`,
		`{"type":"user","isSidechain":false,"isMeta":false,"timestamp":"2026-07-20T10:00:02Z","message":{"role":"user","content":"how do I fix this deploy error?"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("could not write fixture session file: %v", err)
	}

	got, err := sessionLabel(path)
	if err != nil {
		t.Fatalf("sessionLabel returned error: %v", err)
	}
	if got != "how do I fix this deploy error?" {
		t.Errorf("expected the first genuine user message as the snippet, got %q", got)
	}
}

// TestSessionLabel_FallsBackWhenOnlySyntheticMessagesExist confirms a
// session made up entirely of slash-command noise (no real free-text user
// turn, and no ai-title line) still returns the existing "(no user
// message)" fallback rather than a raw command envelope.
func TestSessionLabel_FallsBackWhenOnlySyntheticMessagesExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"user","isSidechain":false,"isMeta":false,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"<command-name>/model</command-name>\n            <command-message>model</command-message>\n            <command-args></command-args>"}}`,
		`{"type":"assistant","isSidechain":false,"isMeta":false,"timestamp":"2026-07-20T10:00:01Z","message":{"role":"assistant","content":"Model updated."}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("could not write fixture session file: %v", err)
	}

	got, err := sessionLabel(path)
	if err != nil {
		t.Fatalf("sessionLabel returned error: %v", err)
	}
	if got != "(no user message)" {
		t.Errorf("expected the no-user-message fallback, got %q", got)
	}
}

// TestSessionLabel_PrefersClaudeCodesOwnAITitle is a regression test for
// the actual reported bug: sharetoai push's picker showed the session's
// first message instead of the title Claude Code's own UI displays for
// that session. Claude Code writes a dedicated "ai-title" line (verified
// against real session files on this machine, e.g.
// {"type":"ai-title","aiTitle":"Build ConvoBridge conversation extraction
// tool","sessionId":"..."}) once it has generated a summary — when
// present, sessionLabel must use that exact text rather than any message
// content, even though a perfectly good real user message exists earlier
// in the same file.
func TestSessionLabel_PrefersClaudeCodesOwnAITitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"user","isSidechain":false,"isMeta":false,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"I want to build a high-performance web-based tool called Convobridge."}}`,
		`{"type":"ai-title","aiTitle":"Build ConvoBridge conversation extraction tool","sessionId":"abc-123"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("could not write fixture session file: %v", err)
	}

	got, err := sessionLabel(path)
	if err != nil {
		t.Fatalf("sessionLabel returned error: %v", err)
	}
	if got != "Build ConvoBridge conversation extraction tool" {
		t.Errorf("expected Claude Code's own ai-title, got %q", got)
	}
}

// TestSessionLabel_UsesLatestAITitleWhenRepeated confirms that when
// multiple ai-title lines exist (observed in real session files -- Claude
// Code appears to (re)write the same or an updated title as a session
// progresses), the LAST one wins, not the first.
func TestSessionLabel_UsesLatestAITitleWhenRepeated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"type":"ai-title","aiTitle":"Early draft title","sessionId":"abc-123"}`,
		`{"type":"user","isSidechain":false,"isMeta":false,"timestamp":"2026-07-20T10:00:00Z","message":{"role":"user","content":"more context"}}`,
		`{"type":"ai-title","aiTitle":"Refined final title","sessionId":"abc-123"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("could not write fixture session file: %v", err)
	}

	got, err := sessionLabel(path)
	if err != nil {
		t.Fatalf("sessionLabel returned error: %v", err)
	}
	if got != "Refined final title" {
		t.Errorf("expected the latest ai-title to win, got %q", got)
	}
}

func TestPostLocalHandoffCredit_ReturnsErrorOn402(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"detail":"Not enough credits"}`))
	}))
	defer server.Close()
	t.Setenv("SHARETOAI_API_URL", server.URL)

	err := postLocalHandoffCredit("test-key", "codex")
	if err == nil {
		t.Fatal("expected an error on 402, got nil")
	}
	if !strings.Contains(err.Error(), "Not enough credits") {
		t.Errorf("expected the server's detail message in the error, got: %v", err)
	}
}
