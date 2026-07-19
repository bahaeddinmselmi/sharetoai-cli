package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// randomID mimics OpenCode's own opaque ID style (a short prefix plus a
// random suffix, e.g. "ses_08510ef57ffeXHrOcjEZeNcv7Z") closely enough
// that OpenCode accepts it — verified by a real import round-trip (see
// docs/superpowers/specs/2026-07-19-cli-cross-tool-handoff-design.md).
// OpenCode does not appear to validate the suffix's exact charset, only
// that IDs are present and unique within the file.
func randomID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// openCodeSessionExport mirrors the shape `opencode export` produces and
// `opencode import` accepts, trimmed to the fields that matter for a
// handed-off conversation (runtime-only fields like cost/tokens/
// permission and reasoning/step-start/step-finish parts are omitted —
// confirmed via a real round-trip that this trimmed shape still imports).
type openCodeSessionExport struct {
	Info     openCodeSessionInfo `json:"info"`
	Messages []openCodeMessage   `json:"messages"`
}

type openCodeSessionInfo struct {
	ID        string            `json:"id"`
	Slug      string            `json:"slug"`
	ProjectID string            `json:"projectID"`
	Directory string            `json:"directory"`
	Path      string            `json:"path"`
	Title     string            `json:"title"`
	Agent     string            `json:"agent"`
	Version   string            `json:"version"`
	Time      openCodeTimeRange `json:"time"`
}

type openCodeTimeRange struct {
	Created int64 `json:"created"`
	Updated int64 `json:"updated"`
}

type openCodeMessage struct {
	Info  openCodeMessageInfo `json:"info"`
	Parts []openCodePart      `json:"parts"`
}

type openCodeMessageInfo struct {
	Role      string          `json:"role"`
	Time      openCodeMsgTime `json:"time"`
	ID        string          `json:"id"`
	SessionID string          `json:"sessionID"`
}

type openCodeMsgTime struct {
	Created int64 `json:"created"`
}

type openCodePart struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
}

// writeOpenCodeSession converts a parsed Claude Code transcript into an
// OpenCode-importable JSON file (written to a temp location — `opencode
// import` accepts any file path, so this never touches ~/.opencode
// directly) and returns that file's path.
func writeOpenCodeSession(messages []parsedMessage, cwd string) (string, error) {
	sessionID := randomID("ses_")
	now := time.Now().UnixMilli()

	title := "Handoff from Claude Code"
	for _, m := range messages {
		if m.Role == "user" {
			title = truncate(strings.ReplaceAll(m.Content, "\n", " "), 80)
			break
		}
	}

	out := openCodeSessionExport{
		Info: openCodeSessionInfo{
			ID:        sessionID,
			Slug:      "claude-code-handoff",
			ProjectID: "sharetoai-handoff",
			Directory: cwd,
			Title:     title,
			Agent:     "build",
			Version:   "1.17.9",
			Time:      openCodeTimeRange{Created: now, Updated: now},
		},
	}

	for _, m := range messages {
		msgID := randomID("msg_")
		partID := randomID("prt_")
		out.Messages = append(out.Messages, openCodeMessage{
			Info: openCodeMessageInfo{
				Role:      m.Role,
				Time:      openCodeMsgTime{Created: now},
				ID:        msgID,
				SessionID: sessionID,
			},
			Parts: []openCodePart{
				{Type: "text", Text: m.Content, ID: partID, SessionID: sessionID, MessageID: msgID},
			},
		})
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}

	path := filepath.Join(os.TempDir(), "sharetoai-handoff-"+sessionID+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return "", fmt.Errorf("writing OpenCode handoff file: %w", err)
	}
	return path, nil
}
