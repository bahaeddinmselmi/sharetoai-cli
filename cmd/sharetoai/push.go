package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// outMessage/outConversation mirror the backend's Message/Conversation
// pydantic models (backend/app/models.py) exactly — this is the wire
// format POST /cli/sync expects.
type outMessage struct {
	Role      string            `json:"role"`
	Content   string            `json:"content"`
	Index     int               `json:"index"`
	Timestamp *string           `json:"timestamp,omitempty"`
	Metadata  map[string]string `json:"metadata"`
}

type outConversation struct {
	Platform         string       `json:"platform"`
	SourceURL        *string      `json:"source_url"`
	Title            *string      `json:"title"`
	Messages         []outMessage `json:"messages"`
	ExtractionMethod string       `json:"extraction_method"`
	IsExperimental   bool         `json:"is_experimental"`
	Warnings         []string     `json:"warnings"`
}

func runPush() error {
	apiKey, err := loadApiKey()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	files, err := findSessionFiles(cwd)
	if err != nil {
		return fmt.Errorf("looking for Claude Code sessions: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no Claude Code session found for %s", cwd)
	}

	// A single reader wrapping stdin, shared across every interactive prompt
	// in this run. bufio.Reader reads ahead into its own internal buffer, so
	// two independent readers over the same os.Stdin can race: the first
	// reader's underlying read syscall may pull bytes meant for a later
	// prompt into its own buffer, leaving a second, freshly-constructed
	// reader looking at an already-drained stdin.
	stdin := bufio.NewReader(os.Stdin)

	chosen, err := chooseSessionFile(files, stdin)
	if err != nil {
		return err
	}

	messages, err := parseSessionFile(chosen.Path)
	if err != nil {
		return err
	}

	fmt.Println("Found session. Where do you want to send it?")
	fmt.Println("  [1] Web view link (sharetoai.app)")
	fmt.Println("  [2] OpenCode")
	fmt.Println("  [3] Codex")
	fmt.Println("  [4] Antigravity")
	fmt.Print("> ")
	destination, err := parseDestinationChoice(stdin)
	if err != nil {
		return err
	}

	if destination == "web" {
		conversation := buildConversation(messages)
		fmt.Println("Pushing conversation…")
		viewURL, err := pushToServer(apiKey, conversation)
		if err != nil {
			return err
		}
		fmt.Println(viewURL)
		if err := openBrowser(viewURL); err != nil {
			fmt.Fprintf(os.Stderr, "(couldn't auto-open a browser: %v — open the link above manually)\n", err)
		}
		return nil
	}

	// Produce the deliverable BEFORE charging any credit: dispatch writes
	// the local handoff file (or fails, e.g. a temp-file write error or a
	// ~/.codex mkdir permission failure) with no charge having happened
	// yet. Only once the file genuinely exists on disk do we bill the
	// user for it — never the other way around, which would charge for a
	// handoff the user never received.
	path, runBin, runArgs, hint, err := dispatchLocalDestination(destination, messages, cwd)
	if err != nil {
		return err
	}

	if err := postLocalHandoffCredit(apiKey, destination); err != nil {
		// The file was written successfully but the credit call failed —
		// the user keeps the file they already have and simply wasn't
		// charged for it. That's the safe direction to fail in: no harm
		// done, unlike charging before the file existed.
		return err
	}

	fmt.Printf("Wrote %s\n", path)
	if runBin != "" {
		runOrPrint(runBin, runArgs, hint)
	} else {
		fmt.Println(hint)
	}
	return nil
}

// dispatchLocalDestination writes the handed-off conversation in the file
// format the chosen destination expects and reports how to hand it off from
// there. runBin/runArgs name a command runOrPrint can try to auto-run; an
// empty runBin means there's nothing to auto-run (e.g. Antigravity has no
// import command) and the caller should just print hint. hint is always the
// human-readable fallback/explanation to show either way.
func dispatchLocalDestination(destination string, messages []parsedMessage, cwd string) (path, runBin string, runArgs []string, hint string, err error) {
	switch destination {
	case "opencode":
		path, err = writeOpenCodeSession(messages, cwd)
		if err != nil {
			return "", "", nil, "", err
		}
		return path, "opencode", []string{"import", path}, fmt.Sprintf("Run this to finish importing:\n  opencode import %s", path), nil
	case "codex":
		path, err = writeCodexSession(messages, cwd)
		if err != nil {
			return "", "", nil, "", err
		}
		return path, "codex", []string{"resume", "--last"}, "Run this to resume:\n  codex resume --last", nil
	case "antigravity":
		convID, injectErr := writeAntigravityConversation(messages, cwd)
		if injectErr == nil {
			return convID, "agy", []string{"--conversation", convID}, fmt.Sprintf("Run this to resume:\n  agy --conversation %s", convID), nil
		}
		// Real injection needs a real Antigravity CLI install with the
		// exact on-disk schema this was reverse-engineered against — any
		// mismatch (not installed, schema changed) falls back to the
		// original plain-Markdown handoff rather than failing outright.
		path, err = writeAntigravityHandoff(messages)
		if err != nil {
			return "", "", nil, "", err
		}
		return path, "", nil, "Antigravity has no public API for importing external conversations. Open Antigravity, start a new conversation, and paste this file's content as your first message.", nil
	default:
		return "", "", nil, "", fmt.Errorf("unknown local destination %q", destination)
	}
}

// chooseSessionFile returns the single match outright, or prompts
// interactively when more than one session file was found for this
// project — never guesses on the user's behalf. The caller supplies the
// *bufio.Reader wrapping stdin so that it can be reused for any later
// prompts in the same run; bufio.Reader reads ahead into its own internal
// buffer, so creating a second, independent reader over os.Stdin later in
// the same process would silently drop any input it already buffered.
func chooseSessionFile(files []sessionFile, reader *bufio.Reader) (sessionFile, error) {
	if len(files) == 1 {
		return files[0], nil
	}

	fmt.Printf("Found %d recent sessions. Which one to push?\n", len(files))
	for i, f := range files {
		label, err := sessionLabel(f.Path)
		if err != nil {
			label = "(unreadable)"
		}
		modTime := time.Unix(f.ModTime, 0).Local().Format("2006-01-02 15:04")
		fmt.Printf("  [%d] %s — %s\n", i+1, modTime, label)
	}
	fmt.Print("> ")

	line, _ := reader.ReadString('\n')
	var choice int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &choice); err != nil || choice < 1 || choice > len(files) {
		return sessionFile{}, fmt.Errorf("invalid choice %q", strings.TrimSpace(line))
	}
	return files[choice-1], nil
}

// syntheticUserMessagePrefixes are the opening tags Claude Code uses for
// user-role transcript entries that are not something a human actually
// typed — slash-command invocations and their output, plus background
// task notifications. Verified against real session files on this
// machine: a session opening with a slash command (e.g. `/login`) stores
// it as a literal role="user" message whose content starts with one of
// these, and sessionLabel must not surface that as the picker label.
var syntheticUserMessagePrefixes = []string{
	"<command-name>",
	"<command-message>",
	"<local-command-stdout>",
	"<task-notification>",
}

func isSyntheticUserMessage(content string) bool {
	for _, prefix := range syntheticUserMessagePrefixes {
		if strings.HasPrefix(content, prefix) {
			return true
		}
	}
	return false
}

// aiTitleLine is the raw shape of an "ai-title" transcript line — Claude
// Code's own generated summary for a session, the same text its own UI
// (e.g. the /resume picker) displays. Verified against real session files
// on this machine.
type aiTitleLine struct {
	Type    string `json:"type"`
	AiTitle string `json:"aiTitle"`
}

// latestAITitle scans a transcript for the most recent "ai-title" line and
// returns its text. Claude Code appears to (re)write this line as a
// session progresses — the same title was observed repeated verbatim
// across multiple lines in one real session file, and a later, refined
// title over an earlier draft in another — so the LAST one found wins.
// ok is false if the transcript has no ai-title line at all (observed on a
// real, very short session), in which case the caller should fall back to
// a message-based label.
func latestAITitle(path string) (title string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry aiTitleLine
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type == "ai-title" && strings.TrimSpace(entry.AiTitle) != "" {
			title = entry.AiTitle
			ok = true
		}
	}
	return title, ok
}

// sessionLabel returns the display label for a session in the destination
// picker: Claude Code's own generated title when the transcript has one —
// the same text Claude Code's own UI shows for this session — falling back
// to the first genuine free-text user message when no title has been
// generated yet (e.g. a very short or freshly-started session).
func sessionLabel(path string) (string, error) {
	if title, ok := latestAITitle(path); ok {
		return truncate(title, 60), nil
	}

	messages, err := parseSessionFile(path)
	if err != nil {
		return "", err
	}
	for _, m := range messages {
		if m.Role == "user" && !isSyntheticUserMessage(m.Content) {
			return truncate(strings.ReplaceAll(m.Content, "\n", " "), 60), nil
		}
	}
	return "(no user message)", nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func buildConversation(messages []parsedMessage) outConversation {
	out := make([]outMessage, 0, len(messages))
	var title *string
	for i, m := range messages {
		var ts *string
		if m.Timestamp != "" {
			ts = &m.Timestamp
		}
		out = append(out, outMessage{
			Role:      m.Role,
			Content:   m.Content,
			Index:     i,
			Timestamp: ts,
			Metadata:  map[string]string{},
		})
		if title == nil && m.Role == "user" {
			t := truncate(strings.ReplaceAll(m.Content, "\n", " "), 80)
			title = &t
		}
	}
	return outConversation{
		Platform:         "cli",
		SourceURL:        nil,
		Title:            title,
		Messages:         out,
		ExtractionMethod: "fast_path",
		IsExperimental:   false,
		Warnings:         []string{},
	}
}

func pushToServer(apiKey string, conversation outConversation) (string, error) {
	body, err := json.Marshal(conversation)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, apiBaseURL()+"/cli/sync", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach %s: %w", apiBaseURL(), err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Detail string `json:"detail"`
		}
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Detail != "" {
			return "", fmt.Errorf("%s", apiErr.Detail)
		}
		return "", fmt.Errorf("server returned %s", resp.Status)
	}

	var result struct {
		ViewURL string `json:"view_url"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unexpected response from server: %w", err)
	}
	return result.ViewURL, nil
}

// parseDestinationChoice reads one line and maps it to a destination key.
func parseDestinationChoice(r *bufio.Reader) (string, error) {
	line, _ := r.ReadString('\n')
	choice := strings.TrimSpace(line)
	switch choice {
	case "1":
		return "web", nil
	case "2":
		return "opencode", nil
	case "3":
		return "codex", nil
	case "4":
		return "antigravity", nil
	default:
		return "", fmt.Errorf("invalid choice %q", choice)
	}
}

// postLocalHandoffCredit charges the same credit cost the web-link
// destination pays, without sending any conversation content.
func postLocalHandoffCredit(apiKey, tool string) error {
	body, err := json.Marshal(map[string]string{"tool": tool})
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, apiBaseURL()+"/cli/local-handoff", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s: %w", apiBaseURL(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	var apiErr struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Detail != "" {
		return fmt.Errorf("%s", apiErr.Detail)
	}
	return fmt.Errorf("server returned %s", resp.Status)
}

// runOrPrint auto-runs a target tool's command if it's on PATH; otherwise
// it prints the command for the user to run themselves.
func runOrPrint(bin string, args []string, hint string) {
	if _, err := exec.LookPath(bin); err != nil {
		fmt.Println(hint)
		return
	}
	fmt.Printf("Running: %s %s\n", bin, strings.Join(args, " "))
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "(couldn't auto-run %s: %v)\n%s\n", bin, err, hint)
	}
}
