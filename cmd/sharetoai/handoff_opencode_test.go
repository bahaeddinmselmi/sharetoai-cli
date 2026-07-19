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
	// Every message's info block must carry "agent" — real `opencode import`
	// rejects files missing it on any message (not just the session-level
	// info) with "Missing key at [\"agent\"]".
	for i, m := range out.Messages {
		if m.Info.Agent != "build" {
			t.Errorf("message %d: expected Info.Agent %q, got %q", i, "build", m.Info.Agent)
		}
	}
	// A real `opencode import` round-trip also rejects messages missing
	// "model"/"modelID"+"providerID", "parentID", or "cost"/"tokens" — the
	// user message needs the nested "model" object, the assistant message
	// needs the flat modelID/providerID plus parentID/cost/tokens.
	userInfo := out.Messages[0].Info
	if userInfo.Model == nil || userInfo.Model.ProviderID == "" || userInfo.Model.ModelID == "" {
		t.Errorf("user message missing required Info.Model: %+v", userInfo)
	}
	asstInfo := out.Messages[1].Info
	if asstInfo.ModelID == "" || asstInfo.ProviderID == "" {
		t.Errorf("assistant message missing required Info.ModelID/ProviderID: %+v", asstInfo)
	}
	if asstInfo.ParentID != userInfo.ID {
		t.Errorf("assistant message ParentID = %q, want preceding message ID %q", asstInfo.ParentID, userInfo.ID)
	}
	if asstInfo.Cost == nil {
		t.Errorf("assistant message missing required Info.Cost")
	}
	if asstInfo.Tokens == nil {
		t.Errorf("assistant message missing required Info.Tokens")
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
