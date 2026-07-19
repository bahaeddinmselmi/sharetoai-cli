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

	path, err := writeCodexSession(messages)
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

	var lines []codexRolloutLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line codexRolloutLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("line is not valid JSON: %v (%s)", err, scanner.Text())
		}
		lines = append(lines, line)
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d", len(lines))
	}
	if lines[0].Payload.Role != "user" || lines[0].Payload.Content[0].Text != "explain this function" {
		t.Errorf("first line not preserved correctly: %+v", lines[0])
	}
	if lines[1].Payload.Role != "assistant" || lines[1].Payload.Content[0].Text != "this function does X" {
		t.Errorf("second line not preserved correctly: %+v", lines[1])
	}
}
