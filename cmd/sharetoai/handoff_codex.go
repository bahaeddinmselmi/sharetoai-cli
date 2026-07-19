package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// codexRolloutLine is one line of a Codex CLI ~/.codex/sessions/.../
// rollout-*.jsonl file. The response_item shape below (and the general
// timestamp/type/payload envelope) is now GROUNDED IN A REAL, DIRECTLY
// INSPECTED rollout file from this machine's ~/.codex/sessions/ (written
// by a genuine Codex Desktop/VS Code session, confirmed against
// ~/.codex/config.toml) rather than pure inference. That real file's line
// `type`s, in order, were: session_meta, response_item, event_msg,
// agent_message, user_message, task_started, task_complete, token_count.
// We deliberately only emit session_meta (structural/identifying
// metadata a resume mechanism plausibly needs to recognize the file at
// all) plus response_item (the actual conversation content). We do NOT
// emit event_msg/agent_message/user_message/task_started/task_complete/
// token_count: those look like runtime telemetry/UI-event records from a
// live session, and fabricating "task started/completed" telemetry for a
// conversation that never ran through Codex would be dishonest data, not
// a real improvement.
//
// Still best-effort in one respect: there is no invokable `codex` CLI
// binary in this environment, so the full round-trip — `codex resume
// --last` actually picking up a handed-off file — has not been live
// verified. If resume doesn't pick up a handed-off file correctly,
// revisit whether the omitted telemetry line types turn out to be
// required after all.
type codexRolloutLine struct {
	Timestamp string              `json:"timestamp"`
	Type      string              `json:"type"`
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

// codexSessionMetaLine is the first line of a real Codex rollout file,
// carrying session-identifying metadata. Field names and shape are taken
// directly from a real inspected rollout file (see codexRolloutLine doc
// comment above).
type codexSessionMetaLine struct {
	Timestamp string           `json:"timestamp"`
	Type      string           `json:"type"`
	Payload   codexSessionMeta `json:"payload"`
}

// codexSessionMeta mirrors the real session_meta payload shape. Fields
// that would require fabricating data we don't actually have —
// ModelProvider and BaseInstructions — are omitted via omitempty rather
// than filled with made-up values; an absent field is more honest than
// an invented one, and this is metadata Codex likely doesn't strictly
// need to parse the conversation content itself.
type codexSessionMeta struct {
	ID               string                 `json:"id"`
	Timestamp        string                 `json:"timestamp"`
	Cwd              string                 `json:"cwd"`
	Originator       string                 `json:"originator"`
	CliVersion       string                 `json:"cli_version"`
	Source           string                 `json:"source"`
	ModelProvider    string                 `json:"model_provider,omitempty"`
	BaseInstructions *codexBaseInstructions `json:"base_instructions,omitempty"`
}

type codexBaseInstructions struct {
	Text string `json:"text"`
}

// newCodexSessionID generates a UUIDv7-style identifier (time-ordered,
// 8-4-4-4-12 hex groups with the version/variant nibbles set) matching the
// shape of real Codex rollout filenames, e.g.
// rollout-2026-06-21T18-21-14-019eeb33-64dc-7302-b43c-7b4f10902b02.jsonl.
//
// This is not cosmetic. This package previously used the hardcoded literal
// string "sharetoai-handoff" as this ID, used both as the filename suffix
// and as session_meta.payload.id. That was caught live: `sharetoai push`
// wrote a rollout-*.jsonl file into a real, freshly-installed Codex CLI's
// ~/.codex/sessions/ directory, and a subsequent `codex doctor` run flagged
// it — "rollout DB malformed file names" went from 0 to 1, pointing
// directly at the file this tool wrote, and the health check flipped from
// "✓ threads rollout files and state DB thread inventory agree" to
// "⚠ threads rollout scan was incomplete or found bad files". Codex's
// rollout-directory scanner validates that the segment of the filename
// after the timestamp parses as a real UUID/ULID-style id, and silently
// excludes any file that doesn't from its session index (a SQLite state
// DB) — meaning `codex resume` / `codex resume --last` would never see or
// offer a handed-off file as a session at all, even though the file itself
// looked otherwise well-formed.
//
// Unlike OpenCode's randomID helper (see handoff_opencode.go), which
// OpenCode accepts in a looser, prefixed, human-readable form (e.g.
// "ses_08510ef57ffe..."), Codex requires this exact UUID shape, so a
// human-readable id is not an option here.
func newCodexSessionID(now time.Time) string {
	var b [16]byte
	_, _ = rand.Read(b[:])

	// Stamp the leading 48 bits with the current unix time in
	// milliseconds so the id is time-ordered like real Codex ids
	// (e.g. 019eeb33-64dc-...), then set the UUIDv7 version (0111) and
	// variant (10) bits so the layout matches genuine UUIDs exactly.
	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// writeCodexSession converts a parsed Claude Code transcript into a
// rollout-*.jsonl file under ~/.codex/sessions/YYYY/MM/DD/, matching
// where Codex CLI itself stores sessions (so `codex resume --last` will
// find it as the most recent rollout). cwd is the working directory the
// original conversation happened in, recorded in the session_meta line
// (real Codex rollouts key resume/context off it).
func writeCodexSession(messages []parsedMessage, cwd string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}

	now := time.Now()
	dir := filepath.Join(home, ".codex", "sessions", now.Format("2006"), now.Format("01"), now.Format("02"))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating Codex sessions directory: %w", err)
	}

	// sessionID is the same identifier used both in the filename and in
	// session_meta.id, matching how real Codex rollout filenames embed
	// the session's id alongside its timestamp
	// (rollout-<timestamp>-<id>.jsonl). It must be a real UUID-shaped
	// value — see newCodexSessionID's doc comment for why.
	sessionID := newCodexSessionID(now)
	nowRFC3339 := now.Format(time.RFC3339)

	filename := fmt.Sprintf("rollout-%s-%s.jsonl", now.Format("2006-01-02T15-04-05"), sessionID)
	path := filepath.Join(dir, filename)

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("creating Codex rollout file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)

	metaLine := codexSessionMetaLine{
		Timestamp: nowRFC3339,
		Type:      "session_meta",
		Payload: codexSessionMeta{
			ID:         sessionID,
			Timestamp:  nowRFC3339,
			Cwd:        cwd,
			Originator: "sharetoai push",
			CliVersion: version,
			Source:     "claude-code",
		},
	}
	if err := enc.Encode(metaLine); err != nil {
		return "", fmt.Errorf("writing Codex session_meta line: %w", err)
	}

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
