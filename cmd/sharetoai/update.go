package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const releaseRepo = "bahaeddinmselmi/sharetoai-cli"

type githubRelease struct {
	TagName string `json:"tag_name"`
}

// latestReleaseTag hits GitHub's "latest release" API. apiURL is the full
// endpoint URL, overridable in tests; production calls pass
// githubLatestReleaseURL().
func latestReleaseTag(apiURL string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned %s", resp.Status)
	}

	var release githubRelease
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("unexpected response from GitHub: %w", err)
	}
	return release.TagName, nil
}

func githubLatestReleaseURL() string {
	return "https://api.github.com/repos/" + releaseRepo + "/releases/latest"
}

// releaseAssetName matches the exact naming install.sh/install.ps1 use —
// see sharetoai-cli/install.sh's `asset="sharetoai-${goos}-${goarch}"`
// and install.ps1's `$Asset = "sharetoai-windows-amd64.exe"`.
func releaseAssetName(goos, goarch string) string {
	name := fmt.Sprintf("sharetoai-%s-%s", goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// runUpdate checks the latest GitHub release against the running binary's
// version and, if newer, downloads and replaces the running executable.
// version (main.go) is set at build time via -ldflags "-X main.version=
// ${{ github.ref_name }}" (see .github/workflows/release.yml), so a
// release build's version already carries the "v" prefix (e.g. "v0.1.1")
// matching GitHub's tag_name exactly — no prefix massaging needed. Local
// dev builds default version to "dev", which will never equal a real
// "vX.Y.Z" tag, so `sharetoai update` always attempts an update when run
// from a dev build — expected, since a dev build isn't a tagged release.
func runUpdate() error {
	latest, err := latestReleaseTag(githubLatestReleaseURL())
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}

	if version == latest {
		fmt.Printf("sharetoai is up to date (%s)\n", latest)
		return nil
	}

	asset := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", releaseRepo, latest, asset)

	fmt.Printf("Updating %s → %s...\n", version, latest)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("downloading update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s (asset: %s)", resp.Status, asset)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating running executable: %w", err)
	}

	tmpPath := exePath + ".new"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing downloaded binary: %w", err)
	}
	tmpFile.Close()

	// Verify integrity before ever handing tmpPath to replaceExecutable: the
	// release workflow (.github/workflows/release.yml) publishes a
	// "<asset>.sha256" file alongside every binary asset. This is a fresh
	// download that hasn't touched replaceExecutable yet, so on mismatch
	// it's safe (and correct) to just delete tmpPath ourselves — unlike
	// replaceExecutable's own failure paths, no error message here points
	// the user at tmpPath for manual recovery.
	if err := verifyChecksum(url+".sha256", asset, hasher.Sum(nil)); err != nil {
		os.Remove(tmpPath)
		return err
	}

	// Note: replaceExecutable owns cleanup of tmpPath itself — it only
	// removes tmpPath when it's genuinely safe to discard (the backup
	// step failed and exePath was never touched). On any other failure,
	// tmpPath is the last copy of the downloaded update and/or the file
	// the error message tells the user to manually move into place, so
	// it must be left on disk. Do not unconditionally os.Remove(tmpPath)
	// here.
	if err := replaceExecutable(tmpPath, exePath); err != nil {
		return err
	}

	fmt.Printf("Updated to %s — installed at %s\n", latest, filepath.Clean(exePath))
	return nil
}

// verifyChecksum downloads checksumURL — the release's "<asset>.sha256" file,
// published by .github/workflows/release.yml via
// `shasum -a 256 "${out}" > "${out}.sha256"`, i.e. one line formatted as
// "<hex-hash>  <filename>\n" — and compares its hash against gotSum (the
// SHA256 actually computed over the bytes that were downloaded). Returns a
// descriptive error on any mismatch, network failure, or malformed checksum
// file, so a corrupted or tampered download is caught before it's ever
// swapped into place as the running binary.
func verifyChecksum(checksumURL, assetName string, gotSum []byte) error {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(checksumURL)
	if err != nil {
		return fmt.Errorf("downloading checksum for %s: %w", assetName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading checksum for %s: server returned %s", assetName, resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading checksum for %s: %w", assetName, err)
	}

	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return fmt.Errorf("checksum file for %s is empty or malformed", assetName)
	}
	want := strings.ToLower(fields[0])
	got := hex.EncodeToString(gotSum)
	if want != got {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s — download may be corrupted or tampered with", assetName, want, got)
	}
	return nil
}

// renameFile is os.Rename, kept as a package-level var so tests can
// substitute a stub that fails on demand — some of the failure modes
// replaceExecutable needs to handle (e.g. the destination rename succeeding
// for the backup but failing for the final swap, due to a transient lock
// from antivirus/indexing) are impractical to trigger portably through the
// real filesystem across platforms.
var renameFile = os.Rename

// replaceExecutable swaps the file at tmpPath into exePath, working around a
// Windows-specific restriction: you cannot rename a file onto the path of a
// currently-executing .exe (the OS keeps that path locked while the process
// runs). What Windows does allow is renaming the running executable itself
// away to a different name — the process's open handle stays valid — so this
// follows the standard Windows self-update pattern (used by tools like
// rclone and restic): first rename the current exePath out of the way to
// exePath+".old" (best-effort — a missing exePath is fine, anything else
// with a file actually present there is a real error), then rename tmpPath
// into the now-vacated exePath. On macOS/Linux the backup rename is not
// strictly required (an in-place rename over a running binary works there),
// but doing it unconditionally keeps the logic — and its test coverage —
// identical across platforms, and still gives us a recovery copy if the
// second rename fails.
//
// replaceExecutable owns all cleanup of tmpPath, because only it knows which
// of the two renames failed:
//   - If the backup rename fails for a real reason (not just "exePath
//     doesn't exist yet"), exePath is untouched — nothing was lost, so
//     tmpPath is no longer needed and replaceExecutable removes it here.
//   - If the backup rename succeeds (or was skipped because exePath never
//     existed) but the second rename fails, exePath is now missing (or was
//     never created), and tmpPath is the only thing standing between the
//     user and a missing sharetoai binary — this function's own error
//     message tells the user to manually move tmpPath into place, so it
//     must NOT be deleted, by this function or by its caller.
func replaceExecutable(tmpPath, exePath string) error {
	backupPath := exePath + ".old"
	if err := renameFile(exePath, backupPath); err != nil {
		if !os.IsNotExist(err) {
			// exePath is untouched; tmpPath isn't needed for recovery.
			os.Remove(tmpPath)
			return fmt.Errorf("backing up current binary before replacing it: %w", err)
		}
		// Nothing exists at exePath yet — nothing to back up, continue.
		backupPath = ""
	}

	if err := renameFile(tmpPath, exePath); err != nil {
		if backupPath != "" {
			return fmt.Errorf("replacing current binary (the previous binary was preserved at %s — you can manually move it back to %s; alternatively, the downloaded update is still at %s and can be manually moved to %s): %w", backupPath, exePath, tmpPath, exePath, err)
		}
		return fmt.Errorf("replacing current binary (you may need to run this from outside the binary's own directory; the downloaded update is still at %s and can be manually moved to %s): %w", tmpPath, exePath, err)
	}

	if backupPath != "" {
		os.Remove(backupPath)
	}

	return nil
}
