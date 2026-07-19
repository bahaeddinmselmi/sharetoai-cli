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
// handed-off conversation (permission and reasoning/step-start/step-finish
// parts are omitted). A real `opencode import` round-trip against a live
// build of this CLI showed that "agent" (every message), "model"/"modelID"
// +"providerID", "parentID", and "cost"/"tokens" are NOT optional — import
// rejects the file with "Missing key" for each one if absent, even though
// they carry no meaningful handoff data here.
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

// openCodeMessageInfo covers both the user-message shape (role "user":
// "model" is a nested {providerID, modelID} object) and the
// assistant-message shape (role "assistant": "modelID"/"providerID" are
// flat, plus "mode"/"path"/"finish") — confirmed by a real captured
// `opencode export` sample where the two roles carry genuinely different
// fields. writeOpenCodeSession only ever populates the branch matching a
// given message's role; the other branch's fields are left zero and
// omitted via `omitempty`.
type openCodeMessageInfo struct {
	Role       string            `json:"role"`
	Time       openCodeMsgTime   `json:"time"`
	Agent      string            `json:"agent"`
	Model      *openCodeModelRef `json:"model,omitempty"`
	ModelID    string            `json:"modelID,omitempty"`
	ProviderID string            `json:"providerID,omitempty"`
	Mode       string            `json:"mode,omitempty"`
	Path       *openCodeMsgPath  `json:"path,omitempty"`
	Finish     string            `json:"finish,omitempty"`
	ParentID   string            `json:"parentID,omitempty"`
	Cost       *float64          `json:"cost,omitempty"`
	Tokens     *openCodeTokens   `json:"tokens,omitempty"`
	ID         string            `json:"id"`
	SessionID  string            `json:"sessionID"`
}

// openCodeTokens is required on assistant messages by real `opencode
// import` even for a trimmed handoff — a zero-valued struct satisfies the
// schema without claiming any real usage occurred.
type openCodeTokens struct {
	Input     int64               `json:"input"`
	Output    int64               `json:"output"`
	Reasoning int64               `json:"reasoning"`
	Cache     openCodeTokensCache `json:"cache"`
}

type openCodeTokensCache struct {
	Read  int64 `json:"read"`
	Write int64 `json:"write"`
}

type openCodeModelRef struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
}

type openCodeMsgPath struct {
	Cwd  string `json:"cwd"`
	Root string `json:"root"`
}

type openCodeMsgTime struct {
	Created   int64 `json:"created"`
	Completed int64 `json:"completed,omitempty"`
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

	const providerID = "opencode"
	const modelID = "claude-code-handoff"

	var prevMsgID string
	for _, m := range messages {
		msgID := randomID("msg_")
		partID := randomID("prt_")
		info := openCodeMessageInfo{
			Role:      m.Role,
			Time:      openCodeMsgTime{Created: now},
			Agent:     "build",
			ID:        msgID,
			SessionID: sessionID,
		}
		if m.Role == "assistant" {
			// Matches the flat modelID/providerID + mode/path/finish shape
			// seen on assistant messages in a real `opencode export` sample,
			// which also always carries the preceding message's ID as
			// "parentID".
			info.Mode = "build"
			info.Path = &openCodeMsgPath{Cwd: cwd, Root: cwd}
			info.ModelID = modelID
			info.ProviderID = providerID
			info.Finish = "stop"
			info.Time.Completed = now
			info.ParentID = prevMsgID
			zeroCost := 0.0
			info.Cost = &zeroCost
			info.Tokens = &openCodeTokens{}
		} else {
			// Matches the nested "model" object seen on user messages in
			// the same real captured sample.
			info.Model = &openCodeModelRef{ProviderID: providerID, ModelID: modelID}
		}
		out.Messages = append(out.Messages, openCodeMessage{
			Info: info,
			Parts: []openCodePart{
				{Type: "text", Text: m.Content, ID: partID, SessionID: sessionID, MessageID: msgID},
			},
		})
		prevMsgID = msgID
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
