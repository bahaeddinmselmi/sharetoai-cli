package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// writeAntigravityHandoff writes the conversation as plain Markdown.
// Antigravity stores conversations as workspace-scoped binary protobuf
// files with no public schema, so this deliberately does not attempt to
// write Antigravity's native format — the CLI instead tells the user to
// paste this file's content as the first message of a new Antigravity
// conversation (see Global Constraints in the implementation plan for why
// this is more accurate than pointing at Antigravity's own "import a
// saved conversation" feature, which is for its own previously-saved
// conversations, not external files).
func writeAntigravityHandoff(messages []parsedMessage) (string, error) {
	var b strings.Builder
	b.WriteString("# Conversation handed off from Claude Code\n\n")
	for _, m := range messages {
		if m.Role == "user" {
			b.WriteString("## User\n\n")
		} else {
			b.WriteString("## Assistant\n\n")
		}
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}

	path := filepath.Join(os.TempDir(), fmt.Sprintf("sharetoai-handoff-antigravity-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(b.String()), 0600); err != nil {
		return "", fmt.Errorf("writing Antigravity handoff file: %w", err)
	}
	return path, nil
}
