package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing downloaded binary: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpPath, exePath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replacing current binary (you may need to run this from outside the binary's own directory, or manually move %s to %s): %w", tmpPath, exePath, err)
	}

	fmt.Printf("Updated to %s — installed at %s\n", latest, filepath.Clean(exePath))
	return nil
}
