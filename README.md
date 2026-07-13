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

**Windows:** download `sharetoai-windows-amd64.exe` from the
[latest release](https://github.com/bahaeddinmselmi/sharetoai-cli/releases/latest)
and put it somewhere on your `PATH` (the install script above targets
macOS/Linux only — plain Git Bash / cmd / PowerShell won't run it, use WSL if
you want the one-liner instead).

**With Go installed (any OS):**

```sh
go install github.com/bahaeddinmselmi/sharetoai-cli@latest
```

The binary installs as `sharetoai`.

## Usage

```sh
# 1. Generate a CLI API key from https://sharetoai.app/account
#    ("CLI API key" section), then store it locally:
sharetoai login

# 2. From inside any project directory with a Claude Code session:
sharetoai push
```

`push` prints the resulting one-time link and opens it in your default
browser.

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
