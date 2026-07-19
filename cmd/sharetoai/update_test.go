package main

import (
	"net/http"
	"net/http/httptest"
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
