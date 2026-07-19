package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// codexRolloutLine is one line of a Codex CLI ~/.codex/sessions/.../
// rollout-*.jsonl file. BEST-EFFORT / UNVERIFIED: Codex CLI has no public
// schema for this format; this mirrors its known Responses-API-based
// architecture. Verify against a real `codex` install and adjust field
// names here if `codex resume --last` doesn't pick up a handed-off file
// correctly.
type codexRolloutLine struct {
	Timestamp string            `json:"timestamp"`
	Type      string            `json:"type"`
	Payload   codexRolloutPayload `json:"payload"`
}

type codexRolloutPayload struct {
	Type    string              `json:"type"`
	Role    string              `json:"role"`
	Content []codexContentBlock `json:"content"`
}

type codexContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// writeCodexSession converts a parsed Claude Code transcript into a
// rollout-*.jsonl file under ~/.codex/sessions/YYYY/MM/DD/, matching
// where Codex CLI itself stores sessions (so `codex resume --last` will
// find it as the most recent rollout).
func writeCodexSession(messages []parsedMessage) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}

	now := time.Now()
	dir := filepath.Join(home, ".codex", "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating Codex sessions directory: %w", err)
	}

	filename := fmt.Sprintf("rollout-%s-sharetoai-handoff.jsonl", now.Format("2006-01-02T15-04-05"))
	path := filepath.Join(dir, filename)

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("creating Codex rollout file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for i, m := range messages {
		ts := m.Timestamp
		if ts == "" {
			ts = now.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		}
		contentType := "input_text"
		if m.Role == "assistant" {
			contentType = "output_text"
		}
		line := codexRolloutLine{
			Timestamp: ts,
			Type:      "response_item",
			Payload: codexRolloutPayload{
				Type: "message",
				Role: m.Role,
				Content: []codexContentBlock{
					{Type: contentType, Text: m.Content},
				},
			},
		}
		if err := enc.Encode(line); err != nil {
			return "", fmt.Errorf("writing Codex rollout line: %w", err)
		}
	}

	return path, nil
}
