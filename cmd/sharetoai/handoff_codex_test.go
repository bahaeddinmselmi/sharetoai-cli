package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteCodexSession_WritesOneJSONLLinePerMessage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir() reads this
	t.Setenv("HOME", home)        // macOS/Linux

	messages := []parsedMessage{
		{Role: "user", Content: "explain this function", Timestamp: "2026-07-19T10:00:00Z"},
		{Role: "assistant", Content: "this function does X", Timestamp: "2026-07-19T10:00:05Z"},
	}

	cwd := `C:\Users\bahae\some-project`
	path, err := writeCodexSession(messages, cwd)
	if err != nil {
		t.Fatalf("writeCodexSession returned error: %v", err)
	}

	if !strings.HasPrefix(path, home) {
		t.Errorf("expected path under %q, got %q", home, path)
	}
	if !strings.HasSuffix(path, ".jsonl") {
		t.Errorf("expected a .jsonl file, got %q", path)
	}

	dateDir := time.Now().Format("2006/01/02")
	if !strings.Contains(filepath.ToSlash(path), dateDir) {
		t.Errorf("expected path to contain today's date %q, got %q", dateDir, path)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("could not open written file: %v", err)
	}
	defer f.Close()

	var rawLines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rawLines = append(rawLines, scanner.Text())
	}

	if len(rawLines) != 3 {
		t.Fatalf("expected 3 JSONL lines (1 session_meta + 2 response_item), got %d", len(rawLines))
	}

	var meta codexSessionMetaLine
	if err := json.Unmarshal([]byte(rawLines[0]), &meta); err != nil {
		t.Fatalf("first line is not valid JSON: %v (%s)", err, rawLines[0])
	}
	if meta.Type != "session_meta" {
		t.Errorf("expected first line type %q, got %q", "session_meta", meta.Type)
	}
	if meta.Payload.ID == "" {
		t.Errorf("expected session_meta.id to be set, got empty")
	}
	if meta.Payload.Cwd != cwd {
		t.Errorf("expected session_meta.cwd %q, got %q", cwd, meta.Payload.Cwd)
	}
	if meta.Payload.Originator != "sharetoai push" {
		t.Errorf("expected session_meta.originator %q, got %q", "sharetoai push", meta.Payload.Originator)
	}
	if meta.Payload.Source != "claude-code" {
		t.Errorf("expected session_meta.source %q, got %q", "claude-code", meta.Payload.Source)
	}
	if meta.Payload.ModelProvider != "" {
		t.Errorf("expected session_meta.model_provider to be omitted, got %q", meta.Payload.ModelProvider)
	}
	if meta.Payload.BaseInstructions != nil {
		t.Errorf("expected session_meta.base_instructions to be omitted, got %+v", meta.Payload.BaseInstructions)
	}

	var lines []codexRolloutLine
	for _, raw := range rawLines[1:] {
		var line codexRolloutLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("line is not valid JSON: %v (%s)", err, raw)
		}
		lines = append(lines, line)
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 response_item JSONL lines, got %d", len(lines))
	}
	if lines[0].Type != "response_item" {
		t.Errorf("expected line type %q, got %q", "response_item", lines[0].Type)
	}
	if lines[0].Payload.Role != "user" || lines[0].Payload.Content[0].Text != "explain this function" {
		t.Errorf("first line not preserved correctly: %+v", lines[0])
	}
	if lines[1].Payload.Role != "assistant" || lines[1].Payload.Content[0].Text != "this function does X" {
		t.Errorf("second line not preserved correctly: %+v", lines[1])
	}
}
