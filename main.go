// Command sharetoai is the CLI for Share to AI's zero-retention handoff
// flow: it finds a local Claude Code session file, serializes only the
// conversation the user picks, and pushes it to a one-time viewing link.
// Nothing is scanned or uploaded in bulk — selection happens entirely on
// this machine before any network call is made.
package main

import (
	"fmt"
	"os"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
// (see .github/workflows/release.yml); "dev" when built locally.
var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "login":
		err = runLogin()
	case "push":
		err = runPush()
	case "-v", "--version", "version":
		fmt.Println("sharetoai " + version)
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "sharetoai: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "sharetoai: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  sharetoai login    Store your CLI API key (generate one at sharetoai.app/account)
  sharetoai push     Push the most recent Claude Code session in this directory`)
}
