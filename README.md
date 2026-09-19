# DigiCLI

DigiCLI is a open source, agentic coding cli designed to be a better alternative to most other agentic cli's.

**Three interaction modes:**
- **Plan** (read only) — Analyze code, explore codebases, ask questions without changes
- **Manual** (approval based) — AI suggests changes, you see what each one would do and approve or deny it
- **Auto** (fully automated) — Will write, read and run commands without asking. 

## Quick Start

```bash
# Install
brew tap 10txn/digicli && brew install digicli
```

```bash
# Suggested install for windows
npm i -g @digicli/cli
```

# Start
to start DigiCLI run it with the `digicli` command in any directory

# Commands to get started
`/help` - The help command lists the available commands
`/settings` - View your settings for DigiCLI
`/models` - View your models
`/model name` - Set your model you would like to use in the conversation
`/clear` - Clear the chat 
`/exit` - Exits DigiCLI

## Why DigiCLI?

- **Local** — DigiCLI is designed to run 100% locally (UNLESS YOU USE NON LOCAL MODELS WITH DIGICLI)

- **Privacy** — DigiCLI is dedicated to maintaining user privacy. We do not send telemtry or anything of the sort. The only connection is to your models endpoint and api.github.com if you enable auto update checking.

- **Mode Selection** — Choose between plan, manual, or auto modes 

- **Safe by default** — Every tool call is checked before it runs. Credentials, system files, shell startup files and the .git directory are refused in every mode

- **Cross platform** — macOS, Linux, Windows

## Features

### Core
- Interactive terminal chat with streaming responses
- Mode switching (plan/manual/auto) via Tab key
- File reading, writing and directory listing, sandboxed to the start directory
- Approval prompts in manual mode, with the whole file viewable before you say yes
- Prompt history, step back through what you have already asked
- Mouse wheel scrolling, and the chat stays where you scrolled it
- Tool calls a local model writes into its reply as text are picked up and run properly
- Easy model switching and configuration
- Interactive settings ui
- Auto update checking

### Safety
- `write_file` replaces one named file. It cannot delete, rename or move anything
- Paths outside the start directory are denied in plan mode and put to you for approval in manual mode
- Approving a path outside the start directory opens that one path for the session only. Changing mode closes them all again
- Credentials (`~/.ssh`, `~/.aws`, `~/.netrc` and the like), system directories, shell startup files, `.git` and DigiCLI's own config are refused in every mode. Auto mode does not reach them and you cannot approve them
- Writes go to a temporary file and are renamed into place, so an interrupted write leaves your file as it was

### Supported Models
- **Local** — DigiCLI is designed to support all models running via ollama. 

- **Cloud** — We are working towards support Claude & OpenAI

### Keys
- `/` — Open the command menu `↑` `↓` to choose, `enter` to pick, `esc` to dismiss
- `enter` — Send the current message
- `↑` `↓` — Step back through your previous messages when the command menu is closed
- `y` / `n` — Approve or deny a pending action, `o` to read the full file first
- `tab` — Cycle mode
- `pgup` / `pgdn` / mouse wheel — Scroll the chat history 1
- `ctrl+c` — Interrupt a reply in progress, or quit when idle

## Architecture

- **Terminal UI** — Bubble Tea (TUI framework)
- **LLM Integration** — Ollama is currently the only LLM supported. This will be expanded soon
- **File Operations** — Read, write and list behind a sandbox that resolves symlinks and rejects any path escaping the start directory
- **Approvals** — Manual mode stops each change in front of you, one call at a time, and tells the model what you decided
- **State Management** — Mode tracking, chat history, configuration

## License

MIT License

Copyright (c) 2026 10txn

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
