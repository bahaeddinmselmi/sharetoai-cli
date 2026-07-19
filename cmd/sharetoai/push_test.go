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
