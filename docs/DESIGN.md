# zed-tmux Design Document

## Problem

Zed's terminal tabs die when the editor window closes, crashes, or an SSH connection drops. Long-running tasks (dev servers, AI agents, builds) are lost with no way to recover.

## Solution Overview

A single-binary Go CLI tool (`zed-tmux`) that intercepts Zed terminal startup via a shell rc guard, bridging each terminal tab to a tmux session. The tmux server process lives independently of Zed, providing session persistence.

Core principle: **all logic lives in Go code**. The shell rc file contains only 3 lines of trigger code. The tmux configuration is embedded in the binary and regenerated on every launch.

## Architecture

```
Zed terminal tab starts
    │
    ▼
Shell reads rc file
    │
    ├── $ZED_TERM empty?  ──→ Not a Zed terminal, rc guard does not fire
    ├── $TMUX non-empty?  ──→ Already inside tmux, rc guard does not fire
    │
    ▼  (both conditions met)
exec ~/.local/bin/zed-tmux    ← shell process replaced by Go binary (exec, no intermediate process)
    │
    ▼
Go binary takes over (main.go runDefault)
    │
    ├── 1. Re-check $ZED_TERM / $TMUX (defensive, duplicates rc guard)
    │       Not met → exit(0)
    │
    ├── 2. exec.LookPath("tmux")
    │       Not found → degradation banner → exec $SHELL
    │
    ├── 3. ensureConfig()
    │       Overwrite ~/.config/zed-tmux/tmux.conf
    │       Write failure → degradation banner → exec $SHELL
    │
    ├── 4. socketName($PWD)
    │       Compute "zed-" + sha256(cwd)[0:8]
    │
    ├── 5. listSessions(socket)
    │       Query all sessions on this socket (5s timeout)
    │       Failure → degradation banner → exec $SHELL
    │
    ├── 6. isTTY() check (ioctl, not stat mode)
    │       Not a TTY → silent exec $SHELL
    │
    ├── 7. Launch bubbletea TUI picker
    │       Shows all sessions; attached ones tagged [attached] and unselectable
    │       User action returns an action:
    │       ├── Attach → syscall.Exec tmux attach-session
    │       ├── Create → syscall.Exec tmux new-session
    │       └── Quit   → exit(0), Zed closes the tab
    │
    ▼
syscall.Exec("tmux", ["-L", socket, "-f", config, ...])
Go process replaced by tmux client — no intermediate process
    │
    ▼
tmux session running
Zed closes / crashes / SSH drops → tmux server keeps running
Reopen Zed → new tab → TUI shows previous sessions → attach to resume
```

## Isolation Strategy

### Per-project tmux server

Uses `tmux -L <socket_name>` to run a separate tmux server process for each project directory.

Socket name computation (`session.go socketName`):

```go
hash := sha256.Sum256([]byte(cwd))
socket = fmt.Sprintf("zed-%x", hash[:4])   // first 4 bytes = 8 hex chars
```

Example:

```
/Users/you/project-a  →  tmux -L zed-a1b2c3d4
/Users/you/project-b  →  tmux -L zed-e5f6g7h8
```

Effects:

- Different projects use different tmux server processes — fully isolated
- `prefix + s` (session list) only shows sessions for the current project
- The user's manual SSH tmux sessions (default socket) are unaffected
- Socket files live at `/tmp/tmux-<uid>/zed-<hash>` (on macOS: `/private/tmp/tmux-<uid>/`)

### Socket name derivation

The Go binary reads `$PWD` at startup (i.e., the Zed terminal's working directory). Zed's `terminal.working_directory` defaults to `current_project_directory`, so all terminal tabs in the same project produce the same socket name.

## Session Naming

Default names are incremental numbers (1, 2, 3). Users can type a custom name in the input box when creating. Existing sessions can be renamed with `r`.

```
Full identifier: socket_name / session_name
  zed-a1b2c3d4 / 1
  zed-a1b2c3d4 / dev-server
  zed-e5f6g7h8 / 1
```

Since projects are already isolated by socket, session names don't need path prefixes.

Incremental numbering (`session.go nextSessionName`): scans all sessions (including attached ones), finds the max pure-numeric name + 1. If all sessions have custom names, starts from 1.

Name validation (`session.go validSessionName`): cannot be empty, cannot contain `.` or `:` (tmux restriction).

## TUI Picker

### Interface

With existing sessions (showing current command):

```
  zed-tmux · /Users/you/project-a

  ▸ 1           node          idle 5min
    2           zsh           idle 3h
    dev-server  python3       idle 2d  [attached]

  ⌘ node --expose-gc ~/.qwen/.../cli.js -c
  ↑↓/click select  enter attach  n new  r rename  d delete  q quit
```

- Each row shows: session name, current command (`pane_current_command`), idle duration
- Multi-window sessions show a count: `3w`
- Selected row has a `▸` marker with full-width blue background highlight
- A fixed detail line below the list asynchronously queries `ps -t <pane_tty>` to show the full command line when the cursor moves to a session
- The detail line layout is fixed (always occupies one row) — content changes don't shift the list
- Commands longer than the terminal width scroll horizontally (marquee) within the reserved line; shorter commands display statically
- Pure shell sessions (zsh/bash) show an empty detail line (the shell itself carries no information)
- Sessions already attached by another terminal are dimmed with a yellow `[attached]` tag; cursor navigation skips them; they cannot be attached, renamed, or deleted
- Mouse click selection is supported (attached rows are not clickable)

With no existing sessions:

```
  zed-tmux · /Users/you/project-a

  No sessions available

  ↑↓/click select  enter attach  n new  r rename  d delete  q quit
```

New / rename enters input mode:

```
  zed-tmux · /Users/you/project-a

  New session name: 3▌

  enter confirm  esc cancel
```

Delete requires confirmation:

```
  zed-tmux · /Users/you/project-a

  Delete session "1"? (y/n)
```

### Keys

| Key | Action |
|---|---|
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `enter` | Attach to selected session |
| `n` | New session (enters input mode, pre-filled with next incremental number) |
| `r` | Rename selected session (enters input mode, pre-filled with current name) |
| `d` | Delete selected session (enters confirm mode) |
| `q` / `esc` | Quit picker, close terminal tab |

Input mode: `enter` confirms, `esc` cancels back to list.
Confirm mode: `y` confirms deletion, `n` / `esc` cancels.

### Display Rules

- Lists all sessions on the current socket (including attached ones)
- Attached sessions are dimmed + yellow `[attached]` tag; cursor navigation skips them; no operations allowed
- Initial cursor position is the first unattached session

### TUI Internal State Machine

The TUI has three modes (`tui.go uiMode`):

```
modeNormal ──n──→ modeInput (new)
modeNormal ──r──→ modeInput (rename)
modeNormal ──d──→ modeConfirm
modeInput  ──enter──→ submit (new returns actionCreate / rename calls tmux then back to modeNormal)
modeInput  ──esc──→ modeNormal
modeConfirm ──y──→ call tmux kill-session → modeNormal
modeConfirm ──n/esc──→ modeNormal
```

Rename and delete operations execute tmux commands inside the TUI, then refresh the session list (`refreshSessions`) without exiting. Only attach, create, and quit exit the TUI and return to `main.go`.

### Detail Line

The detail line uses an async query pattern:

1. **Trigger**: cursor movement (up/down), initial load, rename success, delete success
2. **Query**: `ps -t <tty_name> -o args=` on the session's `pane_tty`, filtering out shell processes (zsh/bash/fish/sh/dash), returning the first non-shell line
3. **Staleness**: a generation counter (`detailGen`) increments on each cursor move; results with stale generations are discarded
4. **Scrolling**: if the command exceeds `detailWidth` (terminal width - 4), a `tea.Tick(80ms)` drives horizontal scrolling at 2 chars/tick
5. **Layout**: the detail line always occupies exactly one row (empty or filled), preventing list displacement

## tmux Configuration

Embedded in `config.go` as a constant string, overwritten to `~/.config/zed-tmux/tmux.conf` on every launch.

```bash
# generated by zed-tmux - do not edit
# this file is overwritten on every zed-tmux startup

set -g status off                                    # hide status bar — looks like a plain terminal
set -g default-terminal "tmux-256color"              # terminal type
set -ag terminal-overrides ",xterm-256color:RGB"     # true color support
unbind s                                             # disable session switching (defensive; -L already isolates)
unbind (
unbind )
unbind L
unbind $
set -g mouse on                                      # mouse support
set -g base-index 1                                  # windows numbered from 1
setw -g pane-base-index 1                            # panes numbered from 1
set -sg escape-time 10                               # reduce escape delay (vim users)
set -g history-limit 50000                           # larger scrollback
```

If the write fails (permissions, etc.), a banner is printed and the tool degrades to `exec $SHELL`. The config file is architecturally essential (hiding the status bar, disabling session switching, etc.) — running without it would produce unexpected tmux behavior, so there is no partial-degradation path.

## Session Lifecycle

### Create

```
tmux -L <socket> -f <config> new-session -s <name> -c <cwd>
```

### Attach

```
tmux -L <socket> -f <config> attach-session -t <name>
```

### Destroy

| Scenario | Trigger |
|---|---|
| Press `d` in TUI | `tmux -L <socket> kill-session -t <name>` |
| Type `exit` closing all windows | Natural tmux behavior: last window closes → session ends |
| `zed-tmux gc` | Cleans up sessions idle beyond a threshold across all projects |
| `zed-tmux kill-all` | `tmux -L <socket> kill-server` — kills all sessions and stops the server |

### Preserve

The following scenarios preserve sessions (natural tmux behavior, no extra code needed):

| Scenario | Reason |
|---|---|
| Zed close / quit | tmux server is an independent process |
| Zed crash | Same |
| SSH disconnect | Same |
| Laptop sleep | Same |

## CLI Commands

### `zed-tmux` (no arguments, default behavior)

TUI picker flow. Invoked automatically by the rc guard; rarely run manually.

### `zed-tmux list`

Non-interactive listing of all sessions across all `zed-*` sockets. Scans `/tmp/tmux-<uid>/` for socket files with the `zed-` prefix.

```
$ zed-tmux list
zed-501ebcd2:
  1           zsh           idle 3h
  2           node          idle 5min  attached

zed-a1b2c3d4:
  dev-server  python        idle 2d
```

### `zed-tmux gc [--dry-run] [--max-idle <duration>]`

Cleans up unattached sessions idle beyond a threshold across all projects.

- `--dry-run`: print only, don't kill
- `--max-idle`: default `7d`; accepts Go duration format (`24h`, `168h`) and extensions (`7d`, `30d`, `1w`)

```
$ zed-tmux gc --dry-run --max-idle 30d
[dry-run] would kill zed-501ebcd2/1 (idle 45d)

$ zed-tmux gc --max-idle 30d
killed zed-501ebcd2/1 (idle 45d)
```

### `zed-tmux kill-all`

Kills all sessions for the current project (`$PWD`'s socket). Executes `tmux -L <socket> kill-server`, which also stops the tmux server process for that socket.

```
$ zed-tmux kill-all
killed all sessions on zed-501ebcd2

$ zed-tmux kill-all    # run again
no sessions to kill
```

### `zed-tmux version`

```
$ zed-tmux version
zed-tmux 0.1.0
```

## Shell Integration

rc file (`.zshrc` / `.bashrc`, same content deployed locally and remotely):

```bash
# zed-tmux: persistent terminal sessions in Zed
if [[ -n "$ZED_TERM" && -z "$TMUX" && -z "$ZED_TMUX_GUARD" ]]; then
    exec ~/.local/bin/zed-tmux
fi
```

- `$ZED_TERM`: Zed injects `ZED_TERM=true` for all terminals (local + remote) via `insert_zed_terminal_env` in `crates/terminal/src/terminal.rs`
- `$TMUX`: set by tmux for shells inside tmux — prevents nesting
- `$ZED_TMUX_GUARD`: set by zed-tmux when degrading to a plain shell — prevents the rc guard from re-triggering in a loop
- `exec`: replaces the shell process with the Go binary — no intermediate process
- Regular SSH logins don't have `$ZED_TERM` and are unaffected
- Place at the **end** of the rc file so other initialization (PATH, aliases, etc.) completes first

## Degradation Behavior

| Condition | Behavior |
|---|---|
| `$ZED_TERM` empty or `$TMUX` non-empty | `exit(0)` — rc guard already intercepted; shouldn't normally reach here |
| tmux not in PATH | Degradation banner + exec `$SHELL` |
| tmux config write failure | Degradation banner + exec `$SHELL` (config is architecturally essential; no partial degradation) |
| `listSessions` failure | Degradation banner + exec `$SHELL` |
| stdin is not a TTY | Silent exec `$SHELL` (nobody is watching — no banner) |
| User presses `q` / `esc` in TUI | `exit(0)` — Zed closes the terminal tab |

Degradation prints a prominent banner (bold yellow on TTY) informing the user they're in a plain shell, not tmux:

```
================================
  Degraded to plain shell
  Reason: tmux not found
  No session persistence
================================
```

Separator width adapts to content. Terminals without a banner are tmux sessions.

`exec $SHELL` degradation ensures the user always gets a usable terminal — zed-tmux failures never result in a tab that opens and immediately closes.

## Implementation Notes

### macOS Platform Specifics

1. **tmux socket directory**: On macOS, tmux uses `/private/tmp/tmux-<uid>/` (`/tmp` is a symlink), not `$TMPDIR` (`/var/folders/...`). `tmuxSocketDir()` hardcodes `/tmp` rather than `os.TempDir()`.
2. **tmux error messages**: On macOS, tmux with no server reports `error connecting to ... (No such file or directory)`; on Linux it reports `no server running on ...`. The code matches both.
3. **TTY detection**: `/dev/null` is also a CharDevice on macOS, so `stat.Mode() & ModeCharDevice` cannot distinguish a real terminal. Uses `golang.org/x/term.IsTerminal()` (ioctl TCGETS/TIOCGETA).

### tmux Command Timeout

`listSessions` uses `context.WithTimeout(5s)` + `exec.CommandContext`. This prevents stale socket files (server dead but socket not cleaned up) from causing tmux connection hangs.

### syscall.Exec Semantics

`execTmux` and `execShell` call `syscall.Exec`, which replaces the current Go process with the target — it does not return. This means:

- No intermediate process; Zed sees the PID as the tmux client or shell directly
- When the tmux client exits (detach or session end), Zed sees the process exit and closes the tab
- If `syscall.Exec` fails (extremely rare), an error is printed and the process exits with code 1

## Design Decisions

| # | Question | Decision | Rationale |
|---|---|---|---|
| 1 | Session naming | Default incremental numbers, customizable, renameable with `r` | Simple; socket already isolates projects |
| 2 | gc default threshold | 7d | Covers weekend disconnect scenarios; supports `--max-idle 30d` |
| 3 | TUI display content | List shows command name + idle; detail line asynchronously shows full command line (ps query); long commands scroll | List stays clean and non-blocking; detail loads on demand with zero perceived latency |
| 4 | `q` quit behavior | Close tab immediately, no confirmation | No work state at startup — nothing to lose |
| 5 | Config file strategy | Overwrite on every launch | Guarantees config matches code; no version management needed |
| 6 | kill-all | Provided | One-command cleanup when a project ends or needs a reset |
| 7 | tmux not found | Print banner + exec $SHELL | User knows the reason; terminal remains usable |
| 8 | No TTY | exec $SHELL | tmux requires a TTY; no workaround |

## File Structure

```
zed-tmux/
├── README.md           # English user documentation
├── README_cn.md        # Chinese user documentation
├── LICENSE             # MIT
├── docs/
│   ├── DESIGN.md       # This document (English)
│   └── DESIGN_cn.md    # Design document (Chinese)
├── go.mod              # Go module definition (module zed-tmux, go 1.25)
├── go.sum              # Dependency checksums
├── main.go             # Entry point + CLI dispatch + degradation logic
├── config.go           # tmux config constant + ensureConfig()
├── session.go          # tmux session operations + socket computation + utilities
├── tui.go              # bubbletea TUI picker (three-mode state machine)
└── gc.go               # gc cleanup + duration parsing
```

### File Responsibilities

**main.go** — Program entry point and CLI command dispatch.

- `main()`: dispatches on `os.Args[1]` to `gc` / `list` / `kill-all` / `version`; no arguments runs `runDefault()`
- `runDefault()`: full TUI flow — env check → tmux lookup → config generation → session query → TTY check → TUI → exec tmux
- `runList()`: iterates all `zed-*` sockets, prints session lists
- `runKillAll()`: executes `tmux kill-server` on the current project's socket
- `execTmux()`: `syscall.Exec` into tmux; Go process is replaced, does not return
- `execShell()`: `syscall.Exec` into `$SHELL`; used for degradation
- `isTTY()`: `term.IsTerminal(int(os.Stdin.Fd()))` — ioctl-based detection

**config.go** — tmux configuration management.

- `tmuxConfigContent`: embedded tmux config constant string
- `ensureConfig()`: `MkdirAll` + `WriteFile` to `~/.config/zed-tmux/tmux.conf`; overwrites every time; returns the config file path

**session.go** — tmux session data model and operations.

- `Session` struct: Name, Attached, Windows, CurrentCommand, CurrentPath, TTY, Activity
- `socketName(cwd)`: sha256 first 4 bytes → `zed-<8hex>`
- `listSessions(socket)`: runs `tmux -L <socket> list-sessions -F <format>` with 5s context timeout; parses TSV output. Returns empty list (not error) when no server is running (`no server running` or `error connecting`)
- `nextSessionName()`: max pure-numeric name + 1
- `killSession()` / `renameSession()`: invoke tmux commands
- `validSessionName()`: validates name (non-empty, no `.` or `:`)
- `findZedSockets()`: scans `/tmp/tmux-<uid>/` for `zed-` prefixed socket files
- `tmuxSocketDir()`: `$TMUX_TMPDIR` or `/tmp` + `tmux-<uid>` (note: on macOS tmux uses `/tmp` i.e. `/private/tmp`, not `$TMPDIR`)
- `formatIdle()`: `just now` / `5m` / `3h` / `2d`

**tui.go** — bubbletea TUI picker.

- Three modes: `modeNormal` (list navigation), `modeInput` (text input), `modeConfirm` (delete confirmation)
- `model.sessions` holds the full session list; attached sessions are displayed but unselectable (cursor skips them)
- Detail line: cursor movement triggers async `pane_tty` + `ps -t` query for the full command line; generation counter discards stale results
- `fetchDetail(tty)`: filters shell processes, returns the first non-shell full command line
- Long commands scroll horizontally within the reserved line (`scrollTickCmd`, 80ms/tick, 2 chars/tick)
- `runTUI()`: creates `tea.Program` (with `WithMouseCellMotion`) and runs it; returns the user's `action` (Attach / Create / Quit)
- Mouse click session selection supported (attached rows not clickable)
- Rename and delete complete inside the TUI (invoke tmux → `refreshSessions()`), without exiting
- Styles: selected row blue background full-width highlight, Faint (path, idle, help bar, attached rows), cyan (detail line with `⌘` prefix), yellow (`[attached]` tag), red (error messages)

**gc.go** — Garbage collection.

- `runGC()`: parses `--dry-run` / `--max-idle` flags; iterates all `zed-*` sockets; kills idle, unattached sessions
- `parseDuration()`: supports Go standard format (`24h`) and extensions (`7d`, `1w`)
