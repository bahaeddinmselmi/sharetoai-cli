package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// realConversationSummariesSchema is the exact CREATE TABLE statement for
// Antigravity CLI's real conversation_summaries table, reverse-engineered
// this session by inspecting the actual file at
// ~/.gemini/antigravity-cli/conversation_summaries.db. Tests use this to
// build a realistic fake install; handoff_antigravity_inject.go's own
// antigravitySummaryColumns list (not this string) is what the pre-flight
// schema check actually validates against.
const realConversationSummariesSchema = "CREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT \"\",`preview` text NOT NULL DEFAULT \"\",`step_count` integer NOT NULL DEFAULT 0,`last_modified_time` datetime NOT NULL,`workspace_uris` text NOT NULL,`status` text NOT NULL DEFAULT \"\",`source` text NOT NULL DEFAULT \"\",`project_id` text NOT NULL DEFAULT \"\",`agent_name` text NOT NULL DEFAULT \"\",`parent_conversation_id` text NOT NULL DEFAULT \"\",`nesting_depth` integer NOT NULL DEFAULT 0,`battle_id` text NOT NULL DEFAULT \"\",`winning_conversation_id` text NOT NULL DEFAULT \"\",`not_fully_idle` numeric NOT NULL DEFAULT false,`killed` numeric NOT NULL DEFAULT false,`last_user_input_time` datetime NOT NULL,`last_user_input_step_index` integer NOT NULL DEFAULT -1,`app_data_dir` text NOT NULL DEFAULT \"\",PRIMARY KEY (`conversation_id`))"

// setupFakeAntigravityCLI creates a minimal ~/.gemini/antigravity-cli/
// directory with a real-shaped (empty) conversation_summaries.db and an
// empty conversations/ directory, matching a real Antigravity CLI install
// before any conversations exist.
func setupFakeAntigravityCLI(t *testing.T, home string) {
	t.Helper()
	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convDir := filepath.Join(cliDir, "conversations")
	if err := os.MkdirAll(convDir, 0700); err != nil {
		t.Fatalf("creating fake antigravity-cli dir: %v", err)
	}

	summariesPath := filepath.Join(cliDir, "conversation_summaries.db")
	db, err := sql.Open("sqlite", summariesPath)
	if err != nil {
		t.Fatalf("creating fake conversation_summaries.db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(realConversationSummariesSchema); err != nil {
		t.Fatalf("creating fake conversation_summaries table: %v", err)
	}
}

func TestWriteAntigravityConversation_WritesResumableConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home) // Windows: os.UserHomeDir() reads this
	t.Setenv("HOME", home)        // macOS/Linux
	setupFakeAntigravityCLI(t, home)

	messages := []parsedMessage{
		{Role: "user", Content: "what does this error mean?", Timestamp: "2026-07-19T10:00:00Z"},
		{Role: "assistant", Content: "it means the port is already in use", Timestamp: "2026-07-19T10:00:05Z"},
	}

	convID, err := writeAntigravityConversation(messages, `C:\Users\bahae\some-project`)
	if err != nil {
		t.Fatalf("writeAntigravityConversation returned error: %v", err)
	}
	if convID == "" {
		t.Fatalf("expected a non-empty conversation ID")
	}

	convPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversations", convID+".db")
	db, err := sql.Open("sqlite", convPath)
	if err != nil {
		t.Fatalf("opening written conversation database: %v", err)
	}
	defer db.Close()

	var trajID, cascadeID string
	var trajType, source int
	if err := db.QueryRow("SELECT trajectory_id, cascade_id, trajectory_type, source FROM trajectory_meta").
		Scan(&trajID, &cascadeID, &trajType, &source); err != nil {
		t.Fatalf("reading trajectory_meta: %v", err)
	}
	if cascadeID != convID {
		t.Errorf("expected trajectory_meta.cascade_id %q, got %q", convID, cascadeID)
	}
	if trajID == "" {
		t.Errorf("expected a non-empty trajectory_id")
	}

	rows, err := db.Query("SELECT idx, step_type, status, step_payload FROM steps ORDER BY idx")
	if err != nil {
		t.Fatalf("reading steps: %v", err)
	}
	defer rows.Close()

	var stepCount int
	for rows.Next() {
		var idx, stepType, status int
		var payload []byte
		if err := rows.Scan(&idx, &stepType, &status, &payload); err != nil {
			t.Fatalf("scanning step row: %v", err)
		}
		if status != 3 {
			t.Errorf("step %d: expected status 3, got %d", idx, status)
		}

		fields := decodeProtoFields(t, payload)
		contentFieldNum := 19
		textFieldNum := 2
		wantStepType := 14
		if messages[idx].Role == "assistant" {
			contentFieldNum = 20
			textFieldNum = 1
			wantStepType = 15
		}
		if stepType != wantStepType {
			t.Errorf("step %d: expected step_type %d, got %d", idx, wantStepType, stepType)
		}
		body, ok := findField(fields, contentFieldNum)
		if !ok {
			t.Fatalf("step %d: expected content field %d to be present", idx, contentFieldNum)
		}
		bodyFields := decodeProtoFields(t, body.Bytes)
		text, ok := findField(bodyFields, textFieldNum)
		if !ok || string(text.Bytes) != messages[idx].Content {
			t.Errorf("step %d: expected content %q, got %+v (ok=%v)", idx, messages[idx].Content, text, ok)
		}
		stepCount++
	}
	if stepCount != len(messages) {
		t.Fatalf("expected %d steps, got %d", len(messages), stepCount)
	}

	summariesPath := filepath.Join(home, ".gemini", "antigravity-cli", "conversation_summaries.db")
	sdb, err := sql.Open("sqlite", summariesPath)
	if err != nil {
		t.Fatalf("opening conversation_summaries.db: %v", err)
	}
	defer sdb.Close()

	var appDataDir, workspaceURIsRaw string
	var storedStepCount int
	if err := sdb.QueryRow(
		"SELECT app_data_dir, step_count, workspace_uris FROM conversation_summaries WHERE conversation_id = ?",
		convID,
	).Scan(&appDataDir, &storedStepCount, &workspaceURIsRaw); err != nil {
		t.Fatalf("reading conversation_summaries row: %v", err)
	}
	if appDataDir != "antigravity-cli" {
		t.Errorf("expected app_data_dir %q, got %q", "antigravity-cli", appDataDir)
	}
	if storedStepCount != len(messages) {
		t.Errorf("expected step_count %d, got %d", len(messages), storedStepCount)
	}
	var workspaceURIs []string
	if err := json.Unmarshal([]byte(workspaceURIsRaw), &workspaceURIs); err != nil {
		t.Fatalf("workspace_uris is not valid JSON: %v (%s)", err, workspaceURIsRaw)
	}
	if len(workspaceURIs) != 1 {
		t.Errorf("expected exactly 1 workspace URI, got %d", len(workspaceURIs))
	}
}

func TestWriteAntigravityConversation_FailsCleanlyWithoutAntigravityCLI(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	// deliberately do not call setupFakeAntigravityCLI — simulates
	// Antigravity CLI not being installed at all.

	messages := []parsedMessage{{Role: "user", Content: "hi", Timestamp: "2026-07-19T10:00:00Z"}}
	_, err := writeAntigravityConversation(messages, `C:\some\project`)
	if err == nil {
		t.Fatalf("expected an error when Antigravity CLI directory doesn't exist")
	}
}

func TestWriteAntigravityConversation_FailsCleanlyOnSchemaMismatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convDir := filepath.Join(cliDir, "conversations")
	if err := os.MkdirAll(convDir, 0700); err != nil {
		t.Fatalf("creating fake antigravity-cli dir: %v", err)
	}
	summariesPath := filepath.Join(cliDir, "conversation_summaries.db")
	db, err := sql.Open("sqlite", summariesPath)
	if err != nil {
		t.Fatalf("creating fake conversation_summaries.db: %v", err)
	}
	// A conversation_summaries table with an unexpected (fewer) column set
	// -- simulating a future Antigravity schema change.
	if _, err := db.Exec("CREATE TABLE `conversation_summaries` (`conversation_id` text, `title` text)"); err != nil {
		t.Fatalf("creating mismatched conversation_summaries table: %v", err)
	}
	db.Close()

	messages := []parsedMessage{{Role: "user", Content: "hi", Timestamp: "2026-07-19T10:00:00Z"}}
	_, err = writeAntigravityConversation(messages, `C:\some\project`)
	if err == nil {
		t.Fatalf("expected an error on conversation_summaries schema mismatch")
	}

	entries, _ := os.ReadDir(convDir)
	if len(entries) != 0 {
		t.Errorf("expected no conversation database to be written on schema mismatch, found %d files", len(entries))
	}
}
