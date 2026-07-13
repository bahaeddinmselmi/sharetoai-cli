package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

	chosen, err := chooseSessionFile(files)
	if err != nil {
		return err
	}

	messages, err := parseSessionFile(chosen.Path)
	if err != nil {
		return err
	}

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

// chooseSessionFile returns the single match outright, or prompts
// interactively when more than one session file was found for this
// project — never guesses on the user's behalf.
func chooseSessionFile(files []sessionFile) (sessionFile, error) {
	if len(files) == 1 {
		return files[0], nil
	}

	fmt.Printf("Found %d recent sessions. Which one to push?\n", len(files))
	for i, f := range files {
		label, err := firstUserMessageSnippet(f.Path)
		if err != nil {
			label = "(unreadable)"
		}
		modTime := time.Unix(f.ModTime, 0).Local().Format("2006-01-02 15:04")
		fmt.Printf("  [%d] %s — %s\n", i+1, modTime, label)
	}
	fmt.Print("> ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	var choice int
	if _, err := fmt.Sscanf(strings.TrimSpace(line), "%d", &choice); err != nil || choice < 1 || choice > len(files) {
		return sessionFile{}, fmt.Errorf("invalid choice %q", strings.TrimSpace(line))
	}
	return files[choice-1], nil
}

func firstUserMessageSnippet(path string) (string, error) {
	messages, err := parseSessionFile(path)
	if err != nil {
		return "", err
	}
	for _, m := range messages {
		if m.Role == "user" {
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
