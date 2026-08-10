# zed-tmux

Persistent terminal sessions for the [Zed](https://zed.dev) editor, powered by [tmux](https://github.com/tmux/tmux).

[中文文档](README_cn.md)

## Why

Zed's terminal tabs are tied to the editor process. Close the window, crash, or lose an SSH connection — your shells die. Long-running tasks (dev servers, AI agents, builds) are gone.

zed-tmux bridges every Zed terminal tab to a tmux session. tmux servers survive independently of the editor, so your sessions persist across restarts, crashes, and disconnects.

## Features

- **Automatic** — a 3-line shell rc guard launches zed-tmux in every Zed terminal; no manual steps
- **TUI session picker** — select, create, rename, and delete sessions with keyboard or mouse
- **Per-project isolation** — each project directory gets its own tmux server via named sockets
- **Detail line** — hover over a session to see the full foreground command line (async `ps` query, horizontal scroll for long commands)
- **Graceful degradation** — if tmux is missing or anything fails, falls back to a plain shell with a visible banner
- **Zero configuration** — tmux config is embedded in the binary and auto-generated on every launch

## Requirements

- [Zed](https://zed.dev) editor
- [tmux](https://github.com/tmux/tmux) 3.0+
- [Go](https://go.dev) 1.21+ (to build)

## Installation

### Pre-built binaries

Download the latest release for your platform from [GitHub Releases](https://github.com/drawing/zed-tmux/releases):

```bash
# Example: macOS ARM64
curl -fsSL https://github.com/drawing/zed-tmux/releases/latest/download/zed-tmux_Darwin_arm64.tar.gz | tar xz
mv zed-tmux ~/.local/bin/
```

### From source

```bash
# Requires Go 1.21+
go install github.com/drawing/zed-tmux@latest

# Or build manually
git clone https://github.com/drawing/zed-tmux.git
cd zed-tmux
go build -o ~/.local/bin/zed-tmux .
```

### Cross-compile for remote servers

```bash
GOOS=linux GOARCH=amd64 go build -o zed-tmux-linux .
scp zed-tmux-linux remote:~/.local/bin/zed-tmux
```

Single static binary — zero runtime dependencies beyond tmux on the target machine.

## Setup (required, one-time)

> [!IMPORTANT]
> After installing the binary, you **must** run the setup below. Without it, zed-tmux will never activate.

### Automated (recommended)

```bash
~/.local/bin/zed-tmux init
```

That's it. It detects your shell, appends the guard block to your rc file with the correct absolute path. Run it on each machine where you use Zed (local and remote).

### Manual

Add the following to the **end** of your `.zshrc` or `.bashrc`:

```bash
# zed-tmux: persistent terminal sessions in Zed
if [[ -n "$ZED_TERM" && -z "$TMUX" && -z "$ZED_TMUX_GUARD" ]]; then
    exec ~/.local/bin/zed-tmux
fi
```

Replace `~/.local/bin/zed-tmux` with the actual path if you installed elsewhere.

The guard only fires inside Zed terminals (`$ZED_TERM`). Regular SSH, iTerm, Terminal.app, etc. are not affected.

## Usage

### TUI Session Picker

Opens automatically when you create a terminal tab in Zed:

```
  zed-tmux · /Users/you/project

  ▸ 1           node          idle 5min
    2           zsh           idle 3h
    dev-server  python3       idle 2d  [attached]

  ⌘ node --expose-gc ~/.qwen/.../cli.js --config prod.yaml
  ↑↓/click select  enter attach  n new  r rename  d delete  q quit
```

| Key | Action |
|---|---|
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `enter` / click | Attach to selected session (confirms first if attached elsewhere) |
| `n` | New session (auto-numbered, instant) |
| `N` | New session with custom name |
| `r` | Rename selected session |
| `d` | Delete selected session (with confirmation) |
| `q` / `esc` | Quit (closes the terminal tab) |

Sessions already attached by another terminal are dimmed with a yellow `[attached]` tag. You can still select them — pressing `enter` will ask for confirmation before detaching the other client and attaching yours. This handles stale attaches caused by SSH disconnects. Rename and delete remain blocked for attached sessions.

The **detail line** below the list shows the full command line of the selected session, fetched asynchronously via `ps -t <pane_tty>`. Pure shell sessions show an empty detail line. Commands longer than the terminal width scroll horizontally (marquee style).

### CLI Commands

```bash
zed-tmux                                   # TUI picker (usually invoked by the rc guard)
zed-tmux init                              # One-time setup: add shell guard to rc file
zed-tmux list                              # List all sessions across all projects
zed-tmux gc [--dry-run] [--max-idle 30d]   # Clean up idle, unattached sessions
zed-tmux kill-all                          # Kill all sessions for the current project
zed-tmux version                           # Print version
```

`gc` defaults to `--max-idle 7d`. Durations accept Go format (`24h`, `168h`) and extensions (`7d`, `1w`).

## Session Lifecycle

| Event | Session survives? |
|---|---|
| Zed close / quit | ✅ Yes |
| Zed crash | ✅ Yes |
| SSH disconnect | ✅ Yes |
| Laptop sleep | ✅ Yes |
| Type `exit` in shell (last window) | ❌ No — session ends naturally |
| Delete in TUI (`d`) | ❌ No |
| `zed-tmux kill-all` | ❌ No — also stops the tmux server |
| `zed-tmux gc` | ❌ No — idle, unattached sessions only |

## Degradation

If anything goes wrong, zed-tmux falls back to a plain shell with a visible banner:

```
================================
  Degraded to plain shell
  Reason: tmux not found
  No session persistence
================================
```

No banner means you're in a tmux session.

| Condition | Behavior |
|---|---|
| tmux not installed | Banner + plain shell |
| Config write failure | Banner + plain shell |
| Session query failure | Banner + plain shell |
| stdin is not a TTY | Silent plain shell (no banner — nobody is watching) |
| User presses `q` / `esc` in TUI | `exit(0)` — Zed closes the tab |

## Dependencies

| Dependency | Purpose |
|---|---|
| [bubbletea](https://github.com/charmbracelet/bubbletea) | TUI framework (Elm architecture) |
| [lipgloss](https://github.com/charmbracelet/lipgloss) | TUI styling (colors, bold, faint) |
| [bubbles](https://github.com/charmbracelet/bubbles) | Text input component |
| [x/term](https://golang.org/x/term) | TTY detection (ioctl-based) |

## Design

See [docs/DESIGN.md](docs/DESIGN.md) for architecture, implementation details, and design decisions.

## License

[MIT](LICENSE)
