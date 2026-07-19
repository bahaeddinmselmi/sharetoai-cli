package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestWriteOpenCodeSession_ProducesImportableShape(t *testing.T) {
	messages := []parsedMessage{
		{Role: "user", Content: "how do I deploy this?", Timestamp: "2026-07-19T10:00:00Z"},
		{Role: "assistant", Content: "here's a step-by-step guide...", Timestamp: "2026-07-19T10:00:05Z"},
	}

	path, err := writeOpenCodeSession(messages, `C:\Users\bahae\convobridge`)
	if err != nil {
		t.Fatalf("writeOpenCodeSession returned error: %v", err)
	}
	defer os.Remove(path)

	if !strings.HasSuffix(path, ".json") {
		t.Fatalf("expected a .json file path, got %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read written file: %v", err)
	}

	var out openCodeSessionExport
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("written file is not valid JSON for openCodeSessionExport: %v", err)
	}

	if out.Info.ID == "" || !strings.HasPrefix(out.Info.ID, "ses_") {
		t.Errorf("expected a ses_-prefixed session ID, got %q", out.Info.ID)
	}
	if out.Info.Directory != `C:\Users\bahae\convobridge` {
		t.Errorf("expected directory to be the passed cwd, got %q", out.Info.Directory)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(out.Messages))
	}
	if out.Messages[0].Info.Role != "user" || out.Messages[0].Parts[0].Text != "how do I deploy this?" {
		t.Errorf("first message not preserved correctly: %+v", out.Messages[0])
	}
	if out.Messages[1].Info.Role != "assistant" || out.Messages[1].Parts[0].Text != "here's a step-by-step guide..." {
		t.Errorf("second message not preserved correctly: %+v", out.Messages[1])
	}
	// Every message/part ID must be unique — OpenCode keys on these.
	seen := map[string]bool{}
	for _, m := range out.Messages {
		if seen[m.Info.ID] {
			t.Errorf("duplicate message ID %q", m.Info.ID)
		}
		seen[m.Info.ID] = true
		for _, p := range m.Parts {
			if seen[p.ID] {
				t.Errorf("duplicate part ID %q", p.ID)
			}
			seen[p.ID] = true
		}
	}
}
