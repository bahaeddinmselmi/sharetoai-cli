package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestLatestReleaseTag_ParsesTagFromGitHubResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name": "v1.4.0", "assets": []}`))
	}))
	defer server.Close()

	tag, err := latestReleaseTag(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tag != "v1.4.0" {
		t.Errorf("got %q, want %q", tag, "v1.4.0")
	}
}

func TestReleaseAssetName_MatchesInstallScriptConvention(t *testing.T) {
	// Must match install.sh's `sharetoai-${goos}-${goarch}` and
	// install.ps1's `sharetoai-windows-amd64.exe` naming exactly, or
	// `sharetoai update` will download a 404.
	cases := []struct {
		goos, goarch, want string
	}{
		{"windows", "amd64", "sharetoai-windows-amd64.exe"},
		{"linux", "amd64", "sharetoai-linux-amd64"},
		{"darwin", "arm64", "sharetoai-darwin-arm64"},
	}
	for _, c := range cases {
		got := releaseAssetName(c.goos, c.goarch)
		if got != c.want {
			t.Errorf("releaseAssetName(%q, %q) = %q, want %q", c.goos, c.goarch, got, c.want)
		}
	}
}

func TestReplaceExecutable_NoExistingBinary(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "app.exe.new")
	exePath := filepath.Join(dir, "app.exe")

	if err := os.WriteFile(tmpPath, []byte("new-binary"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := replaceExecutable(tmpPath, exePath); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exePath after replace: %v", err)
	}
	if string(got) != "new-binary" {
		t.Errorf("exePath contents = %q, want %q", got, "new-binary")
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("tmpPath should no longer exist after successful replace, stat err = %v", err)
	}
	if _, err := os.Stat(exePath + ".old"); !os.IsNotExist(err) {
		t.Errorf(".old backup should not exist when there was nothing to back up, stat err = %v", err)
	}
}

func TestReplaceExecutable_BacksUpAndReplacesExistingBinary(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "app.exe.new")
	exePath := filepath.Join(dir, "app.exe")
	backupPath := exePath + ".old"

	if err := os.WriteFile(exePath, []byte("old-binary"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(tmpPath, []byte("new-binary"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := replaceExecutable(tmpPath, exePath); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}

	got, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("reading exePath after replace: %v", err)
	}
	if string(got) != "new-binary" {
		t.Errorf("exePath contents = %q, want %q", got, "new-binary")
	}
	// The old binary should have been cleaned up on success, not left behind.
	if _, err := os.Stat(backupPath); !os.IsNotExist(err) {
		t.Errorf("backup file should be removed after successful replace, stat err = %v", err)
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("tmpPath should no longer exist after successful replace, stat err = %v", err)
	}
}

func TestReplaceExecutable_MissingTmpFileLeavesBackupForRecovery(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "app.exe.new") // deliberately never created
	exePath := filepath.Join(dir, "app.exe")
	backupPath := exePath + ".old"

	if err := os.WriteFile(exePath, []byte("old-binary"), 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := replaceExecutable(tmpPath, exePath)
	if err == nil {
		t.Fatal("expected an error when tmpPath does not exist, got nil")
	}

	// The original binary should have been preserved under the backup name
	// so the failure is recoverable rather than data-destroying.
	got, readErr := os.ReadFile(backupPath)
	if readErr != nil {
		t.Fatalf("expected backup at %s to exist after failed replace: %v", backupPath, readErr)
	}
	if string(got) != "old-binary" {
		t.Errorf("backup contents = %q, want %q", got, "old-binary")
	}
}
