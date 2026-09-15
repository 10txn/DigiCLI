# DigiCLI

DigiCLI is a open source, agentic coding cli designed to be a better alternative to most other agentic cli's.

**Three interaction modes:**
- **Plan** (read only) — Analyze code, explore codebases, ask questions without changes
- **Manual** (approval based) — AI suggests changes, you approve each action
- **Auto** (fully automated) — Will write, read and run commands without asking. 

## Quick Start

```bash
# Install
brew tap 10txn/digicli && brew install digicli
```

```bash
# Suggested install for windows
npm i -g digicli
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

- **Privacy** — DigiCLI is dedicated to maintaining user privacy. We do not send telemtry or anything of the sort. The only connection is to your models endpoint.

- **Mode Selection** — Choose between plan, manual, or auto modes 

- **Cross platform** — macOS, Linux, Windows

## Features

### Core
- Interactive terminal chat with streaming responses
- Mode switching (plan/manual/auto) via Tab key
- File reading and directory listing, sandboxed to the start directory
-Easy model switching and configuration

### Supported Models
- **Local** — DigiCLI is designed to support all models running via ollama. 

- **Cloud** — We are working towards support Claude & OpenAI

### Keys
- `/` — Open the command menu `↑` `↓` to choose, `enter` to pick, `esc` to dismiss
- `enter` — Send the current message
- `tab` — Cycle mode
- `pgup` / `pgdn` — Scroll the chat history 1
- `ctrl+c` — Interrupt a reply in progress, or quit when idle

## Architecture

- **Terminal UI** — Bubble Tea (TUI framework)
- **LLM Integration** — Ollama is currently the only LLM supported. This will be expanded soon
- **File Operations** — Read and list behind a sandbox that resolves symlinks and rejects any path escaping the start directory
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
