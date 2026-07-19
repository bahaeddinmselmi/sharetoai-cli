package main

import (
	"os"
	"strings"
	"testing"
)

func TestWriteAntigravityHandoff_WritesReadableMarkdown(t *testing.T) {
	messages := []parsedMessage{
		{Role: "user", Content: "what does this error mean?", Timestamp: "2026-07-19T10:00:00Z"},
		{Role: "assistant", Content: "it means the port is already in use", Timestamp: "2026-07-19T10:00:05Z"},
	}

	path, err := writeAntigravityHandoff(messages)
	if err != nil {
		t.Fatalf("writeAntigravityHandoff returned error: %v", err)
	}
	defer os.Remove(path)

	if !strings.HasSuffix(path, ".md") {
		t.Errorf("expected a .md file, got %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read written file: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "what does this error mean?") {
		t.Errorf("expected user message in output, got: %s", content)
	}
	if !strings.Contains(content, "it means the port is already in use") {
		t.Errorf("expected assistant message in output, got: %s", content)
	}
	if !strings.Contains(content, "## User") || !strings.Contains(content, "## Assistant") {
		t.Errorf("expected role headers, got: %s", content)
	}
}
