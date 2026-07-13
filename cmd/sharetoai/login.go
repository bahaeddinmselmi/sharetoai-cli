package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type deviceCodeResponse struct {
	Code            string `json:"code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
}

type devicePollResponse struct {
	Status string `json:"status"`
	APIKey string `json:"api_key"`
}

const devicePollInterval = 2 * time.Second

// runLogin implements a device-authorization flow (the same shape as
// `gh auth login` or an OAuth device grant): no key ever needs to be
// copy-pasted. The CLI mints a one-time code server-side, opens the
// browser to a page that binds that code to whichever account signs in
// there, and polls until it shows up — the browser doesn't even need to
// be on this machine, since the code (not a local session) is what
// carries the result back.
func runLogin() error {
	dc, err := createDeviceCode()
	if err != nil {
		return fmt.Errorf("could not start login: %w", err)
	}

	fmt.Println("Opening your browser to sign in…")
	fmt.Printf("If it doesn't open automatically, visit:\n  %s\n\n", dc.VerificationURL)
	if err := openBrowser(dc.VerificationURL); err != nil {
		fmt.Fprintf(os.Stderr, "(couldn't auto-open a browser: %v — use the link above)\n", err)
	}

	fmt.Println("Waiting for you to finish signing in…")
	key, err := pollDeviceCode(dc.Code, time.Duration(dc.ExpiresIn)*time.Second)
	if err != nil {
		return err
	}

	if err := saveApiKey(key); err != nil {
		return fmt.Errorf("could not save key: %w", err)
	}

	path, _ := credentialsPath()
	fmt.Printf("Signed in — saved to %s\n", path)
	return nil
}

func createDeviceCode() (*deviceCodeResponse, error) {
	req, err := http.NewRequest(http.MethodPost, apiBaseURL()+"/cli/device-code", nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach %s: %w", apiBaseURL(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}

	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("unexpected response from server: %w", err)
	}
	return &dc, nil
}

func pollDeviceCode(code string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 15 * time.Second}

	for time.Now().Before(deadline) {
		time.Sleep(devicePollInterval)

		resp, err := client.Get(apiBaseURL() + "/cli/device-code/" + code)
		if err != nil {
			return "", fmt.Errorf("could not reach %s: %w", apiBaseURL(), err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return "", readErr
		}

		if resp.StatusCode == http.StatusNotFound {
			return "", errors.New("login link expired before you finished signing in — run `sharetoai login` again")
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("server returned %s", resp.Status)
		}

		var poll devicePollResponse
		if err := json.Unmarshal(body, &poll); err != nil {
			return "", fmt.Errorf("unexpected response from server: %w", err)
		}
		if poll.Status == "approved" {
			return poll.APIKey, nil
		}
		// status == "pending": keep polling.
	}

	return "", errors.New("timed out waiting for sign-in — run `sharetoai login` again")
}
