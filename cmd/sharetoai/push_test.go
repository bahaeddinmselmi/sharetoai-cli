package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChooseDestination_ParsesEachValidChoice(t *testing.T) {
	cases := map[string]string{
		"1\n": "web",
		"2\n": "opencode",
		"3\n": "codex",
		"4\n": "antigravity",
	}
	for input, want := range cases {
		got, err := parseDestinationChoice(bufio.NewReader(strings.NewReader(input)))
		if err != nil {
			t.Fatalf("input %q: unexpected error: %v", input, err)
		}
		if got != want {
			t.Errorf("input %q: got %q, want %q", input, got, want)
		}
	}
}

func TestChooseDestination_RejectsInvalidChoice(t *testing.T) {
	_, err := parseDestinationChoice(bufio.NewReader(strings.NewReader("9\n")))
	if err == nil {
		t.Fatal("expected an error for an out-of-range choice, got nil")
	}
}

func TestPostLocalHandoffCredit_SendsToolAndKey(t *testing.T) {
	var gotAuth, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		body := map[string]string{}
		json.NewDecoder(r.Body).Decode(&body)
		gotBody = body["tool"]
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	t.Setenv("SHARETOAI_API_URL", server.URL)

	err := postLocalHandoffCredit("test-key", "opencode")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("expected Authorization header, got %q", gotAuth)
	}
	if gotBody != "opencode" {
		t.Errorf("expected tool=opencode in request body, got %q", gotBody)
	}
}

func TestPostLocalHandoffCredit_ReturnsErrorOn402(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"detail":"Not enough credits"}`))
	}))
	defer server.Close()
	t.Setenv("SHARETOAI_API_URL", server.URL)

	err := postLocalHandoffCredit("test-key", "codex")
	if err == nil {
		t.Fatal("expected an error on 402, got nil")
	}
	if !strings.Contains(err.Error(), "Not enough credits") {
		t.Errorf("expected the server's detail message in the error, got: %v", err)
	}
}
