package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// TestVerifyChecksum_MatchingSumSucceeds exercises verifyChecksum against a
// fake httptest server serving a ".sha256" file in the exact format
// release.yml's `shasum -a 256 "${out}" > "${out}.sha256"` produces
// ("<hex-hash>  <filename>\n"), with a sum that genuinely matches.
func TestVerifyChecksum_MatchingSumSucceeds(t *testing.T) {
	sum := sha256.Sum256([]byte("pretend-this-is-a-real-executable"))
	checksumLine := fmt.Sprintf("%s  sharetoai-linux-amd64\n", hex.EncodeToString(sum[:]))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumLine))
	}))
	defer server.Close()

	if err := verifyChecksum(server.URL, "sharetoai-linux-amd64", sum[:]); err != nil {
		t.Fatalf("expected checksum to verify, got error: %v", err)
	}
}

// TestVerifyChecksum_MismatchedSumFails is the direct regression test for
// the finding: a downloaded binary whose SHA256 doesn't match the published
// .sha256 must be rejected with a clear error, not silently accepted.
func TestVerifyChecksum_MismatchedSumFails(t *testing.T) {
	wrongSum := sha256.Sum256([]byte("some-other-content"))
	checksumLine := fmt.Sprintf("%s  sharetoai-linux-amd64\n", hex.EncodeToString(wrongSum[:]))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumLine))
	}))
	defer server.Close()

	gotSum := sha256.Sum256([]byte("pretend-this-is-a-real-executable"))
	err := verifyChecksum(server.URL, "sharetoai-linux-amd64", gotSum[:])
	if err == nil {
		t.Fatal("expected a checksum mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected a checksum mismatch error, got: %v", err)
	}
}

func TestVerifyChecksum_MalformedChecksumFileFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(""))
	}))
	defer server.Close()

	sum := sha256.Sum256([]byte("x"))
	if err := verifyChecksum(server.URL, "sharetoai-linux-amd64", sum[:]); err == nil {
		t.Fatal("expected an error for an empty/malformed checksum file, got nil")
	}
}

func TestVerifyChecksum_ServerErrorFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	sum := sha256.Sum256([]byte("x"))
	if err := verifyChecksum(server.URL, "sharetoai-linux-amd64", sum[:]); err == nil {
		t.Fatal("expected an error when the checksum file 404s, got nil")
	}
}

// TestChecksumVerification_EndToEndAgainstFakeServer is a real (no mocking
// of hashing/HTTP) live test of the whole pipeline runUpdate now runs: fetch
// a binary asset over HTTP while hashing it — exactly like io.Copy(io.
// MultiWriter(tmpFile, hasher), resp.Body) does in runUpdate — then fetch
// its published .sha256 over HTTP and verify. It proves a genuine download
// verifies successfully, and that corrupting the downloaded bytes (without
// touching the published checksum) is caught.
func TestChecksumVerification_EndToEndAgainstFakeServer(t *testing.T) {
	binary := []byte("pretend-this-is-a-real-sharetoai-executable-payload")
	sum := sha256.Sum256(binary)
	checksumLine := fmt.Sprintf("%s  sharetoai-linux-amd64\n", hex.EncodeToString(sum[:]))

	mux := http.NewServeMux()
	mux.HandleFunc("/sharetoai-linux-amd64", func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	})
	mux.HandleFunc("/sharetoai-linux-amd64.sha256", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(checksumLine))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	// Download + hash in one pass, the same way runUpdate does.
	resp, err := http.Get(server.URL + "/sharetoai-linux-amd64")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()

	hasher := sha256.New()
	got, err := io.ReadAll(io.TeeReader(resp.Body, hasher))
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(got) != string(binary) {
		t.Fatalf("downloaded content mismatch: got %q, want %q", got, binary)
	}

	if err := verifyChecksum(server.URL+"/sharetoai-linux-amd64.sha256", "sharetoai-linux-amd64", hasher.Sum(nil)); err != nil {
		t.Fatalf("expected a genuine download to verify, got error: %v", err)
	}

	// A corrupted download (bit flip, truncation, tampering in transit) must
	// be caught even though the published checksum itself is untouched.
	corruptedHasher := sha256.New()
	corruptedHasher.Write([]byte("this is NOT what the server actually sent"))
	if err := verifyChecksum(server.URL+"/sharetoai-linux-amd64.sha256", "sharetoai-linux-amd64", corruptedHasher.Sum(nil)); err == nil {
		t.Fatal("expected checksum verification to fail for corrupted download bytes, got nil")
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

// TestReplaceExecutable_SecondRenameFailurePreservesTmpFile is the direct
// regression test for the bug: when the backup rename succeeds (exePath is
// moved out of the way) but the final rename (tmpPath -> exePath) fails,
// exePath no longer exists, and replaceExecutable's own error message tells
// the user to manually move tmpPath into place. If tmpPath were deleted
// (whether by replaceExecutable or by its caller) at that point, the
// recovery instructions in the error would be a lie and the user would be
// left with no working sharetoai binary at all. This asserts the file that
// was at tmpPath before the call is still there, unchanged, after the call
// returns its error — not just that an error was returned.
func TestReplaceExecutable_SecondRenameFailurePreservesTmpFile(t *testing.T) {
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

	// Let the first rename (the backup: exePath -> backupPath) go through
	// for real, but force the second rename (tmpPath -> exePath) to fail.
	// This simulates a real-world case (e.g. antivirus/indexing briefly
	// locking the freshly-vacated destination) that's otherwise impractical
	// to trigger portably through the real filesystem.
	callCount := 0
	orig := renameFile
	renameFile = func(oldpath, newpath string) error {
		callCount++
		if callCount == 2 {
			return fmt.Errorf("simulated transient rename failure")
		}
		return orig(oldpath, newpath)
	}
	defer func() { renameFile = orig }()

	err := replaceExecutable(tmpPath, exePath)
	if err == nil {
		t.Fatal("expected an error when the second rename fails, got nil")
	}

	// The core assertion: tmpPath must still exist, with its original
	// contents, after replaceExecutable returns its error.
	got, readErr := os.ReadFile(tmpPath)
	if readErr != nil {
		t.Fatalf("expected tmpPath %s to still exist after failed replace, but it doesn't: %v", tmpPath, readErr)
	}
	if string(got) != "new-binary" {
		t.Errorf("tmpPath contents = %q, want %q", got, "new-binary")
	}

	// exePath should indeed be missing — it was moved to the backup, and
	// the failed second rename didn't restore it. This is the "emergency"
	// half of the scenario: no working binary at exePath right now.
	if _, err := os.Stat(exePath); !os.IsNotExist(err) {
		t.Errorf("exePath should not exist after backup succeeded but replace failed, stat err = %v", err)
	}

	// The backup should also still be present as an alternate recovery
	// path (move backupPath back to exePath instead of tmpPath forward).
	backupGot, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("expected backup at %s to exist: %v", backupPath, err)
	}
	if string(backupGot) != "old-binary" {
		t.Errorf("backup contents = %q, want %q", backupGot, "old-binary")
	}
}

// TestReplaceExecutable_BackupFailureCleansUpTmpFile covers the opposite
// case: the backup rename itself fails for a real reason (not just "exePath
// doesn't exist yet"). exePath is left completely untouched, so there is no
// missing-binary emergency, and tmpPath is no longer needed — this is the
// one failure path where deleting tmpPath is actually correct, and
// replaceExecutable (not its caller) should be the one doing it.
func TestReplaceExecutable_BackupFailureCleansUpTmpFile(t *testing.T) {
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
	// Make backupPath an existing non-empty directory so the backup rename
	// (exePath -> backupPath) fails with a real error, not os.IsNotExist.
	if err := os.Mkdir(backupPath, 0755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupPath, "placeholder"), []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := replaceExecutable(tmpPath, exePath)
	if err == nil {
		t.Fatal("expected an error when the backup rename fails, got nil")
	}

	// exePath must be untouched — nothing was lost in this failure mode.
	got, readErr := os.ReadFile(exePath)
	if readErr != nil {
		t.Fatalf("expected exePath to still exist untouched: %v", readErr)
	}
	if string(got) != "old-binary" {
		t.Errorf("exePath contents = %q, want %q", got, "old-binary")
	}

	// tmpPath is not needed for recovery here, so replaceExecutable should
	// have cleaned it up itself.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("tmpPath should have been removed after a failed backup, stat err = %v", err)
	}
}
