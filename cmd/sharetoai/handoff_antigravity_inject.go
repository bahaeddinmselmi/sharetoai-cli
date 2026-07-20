package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// antigravityConversationSchema creates the 7 tables real Antigravity CLI
// conversation databases use, in the exact shape reverse-engineered from
// real conversation files this session (see docs/superpowers/specs/
// 2026-07-20-antigravity-real-injection-design.md). Only trajectory_meta
// and steps get rows written below — the other 5 tables were empty in
// every real single-branch conversation sampled, and this was live-tested
// as sufficient: a conversation written with only these two tables
// populated was correctly resumed and recalled by a real Antigravity CLI
// install.
var antigravityConversationSchema = []string{
	"CREATE TABLE `trajectory_meta` (`trajectory_id` text,`cascade_id` text,`trajectory_type` integer,`source` integer,PRIMARY KEY (`trajectory_id`))",
	"CREATE TABLE `steps` (`idx` integer,`step_type` integer NOT NULL DEFAULT 0,`status` integer NOT NULL DEFAULT 0,`has_subtrajectory` numeric NOT NULL DEFAULT false,`metadata` blob,`error_details` blob,`permissions` blob,`task_details` blob,`render_info` blob,`step_payload` blob,`step_format` integer NOT NULL DEFAULT 0,PRIMARY KEY (`idx`))",
	"CREATE TABLE `gen_metadata` (`idx` integer,`data` blob,`size` integer NOT NULL DEFAULT 0,PRIMARY KEY (`idx`))",
	"CREATE TABLE `executor_metadata` (`idx` integer,`data` blob,PRIMARY KEY (`idx`))",
	"CREATE TABLE `parent_references` (`idx` integer,`data` blob,PRIMARY KEY (`idx`))",
	"CREATE TABLE `trajectory_metadata_blob` (`id` text DEFAULT \"main\",`data` blob,PRIMARY KEY (`id`))",
	"CREATE TABLE `battle_mode_infos` (`idx` integer,`data` blob,PRIMARY KEY (`idx`))",
}

// antigravitySummaryColumns is the exact column list (order matters — it
// drives both the pre-flight schema check and the INSERT below) of
// Antigravity CLI's real conversation_summaries table.
var antigravitySummaryColumns = []string{
	"conversation_id", "title", "preview", "step_count", "last_modified_time",
	"workspace_uris", "status", "source", "project_id", "agent_name",
	"parent_conversation_id", "nesting_depth", "battle_id", "winning_conversation_id",
	"not_fully_idle", "killed", "last_user_input_time", "last_user_input_step_index",
	"app_data_dir",
}

// checkAntigravitySummariesSchema opens conversation_summaries.db and
// confirms its conversation_summaries table has exactly the columns this
// writer expects, in the same order. Any mismatch means Antigravity's
// on-disk format has changed since it was reverse-engineered here, and it
// is not safe to write further — the caller falls back to the Markdown
// writer instead of risking a malformed row in the user's real index.
func checkAntigravitySummariesSchema(summariesPath string) error {
	db, err := sql.Open("sqlite", summariesPath)
	if err != nil {
		return fmt.Errorf("opening conversation_summaries.db: %w", err)
	}
	defer db.Close()

	rows, err := db.Query("PRAGMA table_info(conversation_summaries)")
	if err != nil {
		return fmt.Errorf("reading conversation_summaries schema: %w", err)
	}
	defer rows.Close()

	var actual []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue any
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("reading conversation_summaries column info: %w", err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if len(actual) != len(antigravitySummaryColumns) {
		return fmt.Errorf("conversation_summaries has %d columns, expected %d — schema has likely changed", len(actual), len(antigravitySummaryColumns))
	}
	for i, want := range antigravitySummaryColumns {
		if actual[i] != want {
			return fmt.Errorf("conversation_summaries column %d is %q, expected %q — schema has likely changed", i, actual[i], want)
		}
	}
	return nil
}

// writeAntigravityConversation writes messages as a real, resumable
// Antigravity CLI conversation: a new conversation database under
// ~/.gemini/antigravity-cli/conversations/, registered in the CLI's
// conversation_summaries.db index. Returns the new conversation's ID,
// which `agy --conversation <id>` resumes.
//
// This mechanism was reverse-engineered and live-verified against a real
// installed Antigravity CLI — see docs/superpowers/specs/2026-07-20-
// antigravity-real-injection-design.md for the investigation. It relies on
// an undocumented on-disk format that could change in a future Antigravity
// release; checkAntigravitySummariesSchema is the guard against that, and
// the caller (dispatchLocalDestination in push.go) falls back to the
// plain-Markdown writeAntigravityHandoff on any error from this function.
func writeAntigravityConversation(messages []parsedMessage, cwd string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}

	cliDir := filepath.Join(home, ".gemini", "antigravity-cli")
	convDir := filepath.Join(cliDir, "conversations")
	summariesPath := filepath.Join(cliDir, "conversation_summaries.db")

	if _, err := os.Stat(convDir); err != nil {
		return "", fmt.Errorf("Antigravity CLI conversations directory not found: %w", err)
	}
	if _, err := os.Stat(summariesPath); err != nil {
		return "", fmt.Errorf("Antigravity CLI conversation_summaries.db not found: %w", err)
	}
	if err := checkAntigravitySummariesSchema(summariesPath); err != nil {
		return "", err
	}

	convID := newAntigravityUUID()
	trajID := newAntigravityUUID()

	tmp, err := os.CreateTemp("", "sharetoai-antigravity-conv-*.db")
	if err != nil {
		return "", fmt.Errorf("creating temp conversation database: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath) // sqlite creates its own file at this path on first open

	if err := writeAntigravityConversationDB(tmpPath, convID, trajID, messages); err != nil {
		os.Remove(tmpPath)
		return "", err
	}

	finalPath := filepath.Join(convDir, convID+".db")
	if err := os.Rename(tmpPath, finalPath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("moving conversation database into place: %w", err)
	}

	if err := registerAntigravityConversation(summariesPath, convID, messages, cwd); err != nil {
		return "", fmt.Errorf("registering conversation in conversation_summaries.db: %w", err)
	}

	return convID, nil
}

func writeAntigravityConversationDB(path, convID, trajID string, messages []parsedMessage) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("creating conversation database: %w", err)
	}
	defer db.Close()

	for _, stmt := range antigravityConversationSchema {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("creating conversation database schema: %w", err)
		}
	}

	if _, err := db.Exec(
		"INSERT INTO trajectory_meta (trajectory_id, cascade_id, trajectory_type, source) VALUES (?, ?, ?, ?)",
		trajID, convID, 4, 17,
	); err != nil {
		return fmt.Errorf("writing trajectory_meta: %w", err)
	}

	now := time.Now()
	for i, m := range messages {
		when := now.Add(-time.Duration(len(messages)-i) * 30 * time.Second)
		var stepType int
		var payload []byte
		if m.Role == "user" {
			stepType = 14
			payload = antigravityUserStepPayload(trajID, convID, i, m.Content, when)
		} else {
			stepType = 15
			payload = antigravityModelStepPayload(trajID, convID, i, m.Content, when)
		}
		if _, err := db.Exec(
			"INSERT INTO steps (idx, step_type, status, has_subtrajectory, step_payload, step_format) VALUES (?, ?, ?, ?, ?, ?)",
			i, stepType, 3, false, payload, 0,
		); err != nil {
			return fmt.Errorf("writing step %d: %w", i, err)
		}
	}
	return nil
}

func registerAntigravityConversation(summariesPath, convID string, messages []parsedMessage, cwd string) error {
	db, err := sql.Open("sqlite", summariesPath)
	if err != nil {
		return err
	}
	defer db.Close()

	preview := ""
	for _, m := range messages {
		if m.Role == "user" {
			preview = truncate(strings.ReplaceAll(m.Content, "\n", " "), 60)
			break
		}
	}

	now := time.Now().UTC().Format("2006-01-02 15:04:05.0000000+00:00")
	workspaceURIs, err := json.Marshal([]string{"file://" + filepath.ToSlash(cwd)})
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`INSERT INTO conversation_summaries
			(conversation_id, title, preview, step_count, last_modified_time,
			 workspace_uris, status, source, project_id, agent_name,
			 parent_conversation_id, nesting_depth, battle_id, winning_conversation_id,
			 not_fully_idle, killed, last_user_input_time, last_user_input_step_index,
			 app_data_dir)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		convID, "", preview, len(messages), now,
		string(workspaceURIs), "", "", "default-cli-project", "",
		"", 0, "", "",
		false, false, now, -1,
		"antigravity-cli",
	)
	return err
}
