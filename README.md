# sharetoai-cli

The command-line companion to [Share to AI](https://sharetoai.app) — pushes a
local Claude Code terminal session to a one-time viewing link, without ever
uploading anything until you explicitly run it.

Selection happens entirely on your machine: the CLI finds the most recent
Claude Code session file for the current project, lets you pick if more than
one was recently active, and sends only that one conversation. The backend
holds it in memory for at most 10 minutes and deletes it after the first
view — nothing is ever written to a permanent database.

## Install

**macOS / Linux:**

```sh
curl -fsSL https://sharetoai.app/install.sh | sh
```

**Windows (PowerShell):**

```powershell
irm https://sharetoai.app/install.ps1 | iex
```

**With Go installed (any OS):**

```sh
go install github.com/bahaeddinmselmi/sharetoai-cli/cmd/sharetoai@latest
```

The binary installs as `sharetoai` in `$(go env GOPATH)/bin` (typically
`~/go/bin` or `%USERPROFILE%\go\bin` on Windows) — make sure that directory
is on your `PATH`. **If you just added it to `PATH`, open a new terminal
window** — an already-running shell won't pick up the change.

## Usage

```sh
sharetoai login   # opens your browser to sign in — no key to copy or paste
sharetoai push    # from inside any project directory with a Claude Code session
```

`sharetoai login` uses a device-authorization flow, the same shape as `gh
auth login`: it opens `sharetoai.app/cli-login` in your browser, waits for
you to sign in there (magic link, no password), and picks up the resulting
key automatically. `push` prints the resulting one-time link and opens it
in your default browser.

## How it finds a session

Claude Code stores sessions at `~/.claude/projects/<sanitized-cwd>/*.jsonl`,
where the directory name is your project's absolute path with `\`, `/`, and
`:` replaced by `-`. The CLI computes that directory from your current
working directory; if the guess doesn't match anything, it falls back to
scanning every project directory and matching each session file's own
embedded `cwd` field instead.

Only Claude Code sessions are supported today.

## What this CLI does *not* do

It doesn't read or upload anything beyond the one session file you're
pushing — no bulk scanning, no background sync. The actual conversation
extraction logic (for ChatGPT/Claude/Gemini share links) lives entirely in
Share to AI's backend and isn't part of this repo.

## License

MIT
