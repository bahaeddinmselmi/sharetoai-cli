package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sessionFile is one candidate Claude Code transcript found on disk.
type sessionFile struct {
	Path    string
	ModTime int64 // unix seconds, for sorting/display
}

// claudeProjectsDir returns ~/.claude/projects, where Claude Code stores
// one subdirectory per working directory it's been run in.
func claudeProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// sanitizeCwd mirrors the directory-naming scheme Claude Code uses: the
// absolute cwd with every path separator replaced by "-" (e.g.
// C:\Users\bahae\convobridge -> C--Users-bahae-convobridge). This is
// inferred from observed on-disk behavior, not documented, so callers must
// treat it as a first guess and fall back to matching the "cwd" field
// embedded in each session file if the guessed directory doesn't exist or
// yields nothing.
func sanitizeCwd(cwd string) string {
	replacer := strings.NewReplacer(`\`, "-", "/", "-", ":", "-")
	return replacer.Replace(cwd)
}

// findSessionFiles locates every top-level *.jsonl transcript for the
// current working directory, most recently modified first. It never reads
// (or even lists) any session belonging to a different project.
func findSessionFiles(cwd string) ([]sessionFile, error) {
	projectsDir, err := claudeProjectsDir()
	if err != nil {
		return nil, err
	}

	// Primary guess: the naming-convention directory.
	guessDir := filepath.Join(projectsDir, sanitizeCwd(cwd))
	if files, err := listJSONLFiles(guessDir); err == nil && len(files) > 0 {
		return files, nil
	}

	// Fallback: the guessed directory name was wrong (or Claude Code
	// changes its scheme in the future) — scan every project directory and
	// keep only files whose own embedded "cwd" field matches exactly.
	// Never opens files outside ~/.claude/projects, and never touches
	// nested subagent transcripts (only direct children of a project dir).
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}
	var matches []sessionFile
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, entry.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(dir, f.Name())
			if fileMatchesCwd(path, cwd) {
				info, err := f.Info()
				if err != nil {
					continue
				}
				matches = append(matches, sessionFile{Path: path, ModTime: info.ModTime().Unix()})
			}
		}
	}
	sortByModTimeDesc(matches)
	return matches, nil
}

func listJSONLFiles(dir string) ([]sessionFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []sessionFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, sessionFile{Path: filepath.Join(dir, e.Name()), ModTime: info.ModTime().Unix()})
	}
	sortByModTimeDesc(files)
	return files, nil
}

func sortByModTimeDesc(files []sessionFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].ModTime > files[j].ModTime })
}

// fileMatchesCwd peeks at the first JSON line of a transcript and checks
// its "cwd" field, without loading the whole file.
func fileMatchesCwd(path, cwd string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var probe struct {
			Cwd string `json:"cwd"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			continue
		}
		if probe.Cwd == "" {
			continue // not every line carries cwd; keep scanning
		}
		return probe.Cwd == cwd
	}
	return false
}

// rawEntry mirrors the fields we actually care about in a Claude Code
// transcript line — verified against a real session file rather than
// guessed. Every other line type (mode, permission-mode, attachment,
// file-history-snapshot, ai-title, system, queue-operation, agent-name,
// last-prompt, ...) is ignored.
type rawEntry struct {
	Type        string      `json:"type"`
	IsSidechain bool        `json:"isSidechain"`
	IsMeta      bool        `json:"isMeta"`
	Timestamp   string      `json:"timestamp"`
	Message     *rawMessage `json:"message"`
}

type rawMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type rawContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// parsedMessage is this CLI's internal shape, converted to the backend's
// Message schema at POST time.
type parsedMessage struct {
	Role      string
	Content   string
	Timestamp string
}

// parseSessionFile reads one transcript and extracts real chat turns only:
// type "user"/"assistant", not a sidechain (subagent) message, not a meta
// (hook/system) message, with non-empty text content. thinking/tool_use/
// tool_result blocks are dropped — this is a handoff of the conversation,
// not a full execution trace.
func parseSessionFile(path string) ([]parsedMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []parsedMessage
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry rawEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}
		if entry.IsSidechain || entry.IsMeta || entry.Message == nil {
			continue
		}

		text := extractText(entry.Message.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		messages = append(messages, parsedMessage{
			Role:      entry.Message.Role,
			Content:   text,
			Timestamp: entry.Timestamp,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("no chat messages found in %s", path)
	}
	return messages, nil
}

// extractText handles both content shapes seen in real transcripts: a
// plain string (typical for simple user turns) or an array of content
// blocks (assistant turns, or user turns carrying tool results/
// attachments alongside text) — only "text" blocks are kept.
func extractText(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}

	var blocks []rawContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}
