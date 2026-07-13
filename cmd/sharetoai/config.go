package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// apiBaseURL points at the public backend directly — the CLI isn't a
// browser, so it skips the Next.js proxy layer the web app uses (no cookie
// forwarding needed, just a bearer token). Overridable for local testing.
func apiBaseURL() string {
	if v := os.Getenv("SHARETOAI_API_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://api.sharetoai.app"
}

func credentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".sharetoai", "credentials"), nil
}

func saveApiKey(key string) error {
	path, err := credentialsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	// 0600: this file holds a live bearer credential, readable only by the
	// owning user.
	return os.WriteFile(path, []byte(strings.TrimSpace(key)+"\n"), 0600)
}

func loadApiKey() (string, error) {
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("not logged in — run `sharetoai login` first")
		}
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("credentials file is empty — run `sharetoai login` again")
	}
	return key, nil
}
