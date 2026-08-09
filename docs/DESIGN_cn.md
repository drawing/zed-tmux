# zed-tmux 设计文档

## 问题

Zed 的终端 tab 在关闭窗口、崩溃、远程 SSH 断连后，shell 进程直接死亡，无法恢复。长时间运行的任务（dev server、AI agent、编译）全部丢失。

## 方案概述

一个 Go 单文件 CLI 工具 `zed-tmux`，在 Zed 终端启动时通过 shell rc 守卫自动介入，将每个终端 tab 桥接到一个 tmux session。tmux server 进程独立于 Zed 存活，实现会话持久化。

核心原则：**所有逻辑在 Go 代码中**，shell rc 文件只有 3 行触发代码，tmux 配置内嵌在 Go 中每次启动覆盖生成。

## 架构

```
Zed 终端 tab 启动
    │
    ▼
shell 读取 rc 文件
    │
    ├── $ZED_TERM 为空？ ──→ 不是 Zed 终端，rc 守卫不触发，正常启动 shell
    ├── $TMUX 非空？   ──→ 已在 tmux 内，rc 守卫不触发，防止嵌套
    │
    ▼ （两个条件都满足）
exec ~/.local/bin/zed-tmux    ← shell 进程被 Go 二进制替换（exec，不留中间进程）
    │
    ▼
Go 二进制接管（main.go runDefault）
    │
    ├── 1. 再次检查 $ZED_TERM / $TMUX（防御性，与 rc 守卫重复）
    │       不满足 → exit(0)
    │
    ├── 2. exec.LookPath("tmux")
    │       找不到 → 降级 banner → exec $SHELL
    │
    ├── 3. ensureConfig()
    │       覆盖写入 ~/.config/zed-tmux/tmux.conf
    │       写入失败 → 降级 banner → exec $SHELL
    │
    ├── 4. socketName($PWD)
    │       计算 "zed-" + sha256(当前目录)[0:8]
    │
    ├── 5. listSessions(socket)
    │       查询该 socket 下的所有 session（5 秒超时）
    │       失败 → 降级 banner → exec $SHELL
    │
    ├── 6. isTTY() 检查（ioctl，不是 stat mode）
    │       非 TTY → 静默 exec $SHELL
    │
    ├── 7. 弹出 bubbletea TUI 选择器
    │       显示所有 session，已 attach 的标 [attached] 且不可选择
    │       用户操作后返回一个 action：
    │       ├── Attach → syscall.Exec tmux attach-session
    │       ├── Create → syscall.Exec tmux new-session
    │       └── Quit   → exit(0)，Zed 关闭该 tab
    │
    ▼
syscall.Exec("tmux", ["-L", socket, "-f", config, ...])
Go 进程被 tmux client 替换，无中间进程
    │
    ▼
tmux session 运行中
Zed 关闭 / 崩溃 / SSH 断连 → tmux server 继续存活
重新打开 Zed → 新 tab → TUI 中看到之前的 session → attach 恢复
```

## 隔离策略

### 按项目隔离 tmux server

使用 `tmux -L <socket_name>` 为每个项目目录启动独立的 tmux server 进程。

socket name 计算方式（`session.go socketName`）：

```go
hash := sha256.Sum256([]byte(cwd))
socket = fmt.Sprintf("zed-%x", hash[:4])   // 取前 4 字节 = 8 个 hex 字符
```

示例：

```
/Users/you/project-a  →  tmux -L zed-a1b2c3d4
/Users/you/project-b  →  tmux -L zed-e5f6g7h8
```

效果：

- 不同项目使用不同的 tmux server 进程，完全隔离
- `prefix + s`（session 列表）只看到当前项目的 session
- 用户手动 SSH 进去用的 tmux（default socket）不受影响
- socket 文件位于 `/tmp/tmux-<uid>/zed-<hash>`（macOS 上是 `/private/tmp/tmux-<uid>/`）

### socket name 的确定

Go 二进制启动时取 `$PWD`（即 Zed 终端的工作目录）。Zed 的 `terminal.working_directory` 默认是 `current_project_directory`，所以同一个项目的所有终端 tab 会得到相同的 socket name。

## Session 命名

默认使用递增序号（1, 2, 3），新建时用户可以在输入框中修改为自定义名称。已有 session 可以按 `r` 重命名。

```
完整标识：socket_name / session_name
  zed-a1b2c3d4 / 1
  zed-a1b2c3d4 / dev-server
  zed-e5f6g7h8 / 1
```

因为已经按 socket 隔离了项目，session name 不需要再带路径前缀。

递增序号的计算方式（`session.go nextSessionName`）：遍历所有 session（包括已 attach 的），找到纯数字名称的最大值 +1。如果所有 session 都是自定义名称，从 1 开始。

名称校验（`session.go validSessionName`）：不能为空，不能包含 `.` 或 `:`（tmux 限制）。

## TUI 选择器

### 界面

有存量 session 时（显示当前运行命令）：

```
  zed-tmux · /Users/you/project-a

  ▸ 1           node          idle 5min
    2           zsh           idle 3h
    dev-server  python3       idle 2d  [attached]

  ⌘ node --expose-gc ~/.qwen/.../cli.js -c
  ↑↓/click select  enter attach  n new  r rename  d delete  q quit
```

- 每行显示：session 名称、当前运行命令（`pane_current_command`）、idle 时长
- 多 window 时额外显示数量：`3w`
- 选中行有 `▸` 标记，蓝色背景全宽高亮
- 列表下方有固定的详情预留行，光标移到某 session 时异步查询 `ps -t <pane_tty>` 显示完整命令行
- 详情行布局固定（始终占一行），不随内容变化导致列表位移
- 命令超长时在预留行内水平滚动（marquee），短于终端宽度时静态显示
- 纯 shell session（zsh/bash）详情行为空（shell 本身没有信息量）
- 已被其他终端 attach 的 session 整行 dim 显示，末尾标黄色 `[attached]`，光标导航跳过，不可 attach/重命名/删除
- 支持鼠标点击选择（attached 行不可点击）

无存量 session 时：

```
  zed-tmux · /Users/you/project-a

  No sessions available

  ↑↓/click select  enter attach  n new  r rename  d delete  q quit
```

新建 / 重命名时进入输入模式：

```
  zed-tmux · /Users/you/project-a

  New session name: 3▌

  enter confirm  esc cancel
```

删除时二次确认：

```
  zed-tmux · /Users/you/project-a

  Delete session "1"? (y/n)
```

### 按键

| 按键 | 行为 |
|---|---|
| `↑` / `k` | 上移光标 |
| `↓` / `j` | 下移光标 |
| `enter` | attach 到选中 session |
| `n` | 新建 session（进入输入模式，预填下一个递增序号） |
| `r` | 重命名选中 session（进入输入模式，预填当前名称） |
| `d` | 删除选中 session（进入确认模式） |
| `q` / `esc` | 退出选择器，关闭终端 tab |

输入模式中：`enter` 确认，`esc` 取消回到列表。
确认模式中：`y` 确认删除，`n` / `esc` 取消。

### 显示规则

- 列出当前 socket 下的所有 session（包括已 attach 的）
- 已 attach 的 session 整行 dim + 黄色 `[attached]` 标记，光标导航跳过，不可操作
- 光标初始位置为第一个未 attach 的 session

### TUI 内部状态机

TUI 有三个模式（`tui.go uiMode`）：

```
modeNormal ──n──→ modeInput（新建）
modeNormal ──r──→ modeInput（重命名）
modeNormal ──d──→ modeConfirm
modeInput  ──enter──→ 提交（新建返回 actionCreate / 重命名调用 tmux 后回到 modeNormal）
modeInput  ──esc──→ modeNormal
modeConfirm ──y──→ 调用 tmux kill-session → modeNormal
modeConfirm ──n/esc──→ modeNormal
```

重命名和删除操作在 TUI 内部直接调用 tmux 命令完成，然后刷新 session 列表（`refreshSessions`），不退出 TUI。只有 attach、create、quit 会退出 TUI 返回给 `main.go`。

### 详情行

详情行使用异步查询模式：

1. **触发时机**：光标移动（上/下）、初始加载、重命名成功、删除成功
2. **查询方式**：对 session 的 `pane_tty` 执行 `ps -t <tty_name> -o args=`，过滤 shell 进程（zsh/bash/fish/sh/dash），返回第一个非 shell 行
3. **过期丢弃**：generation 计数器（`detailGen`）在每次光标移动时递增，收到结果时检查 generation，过期的直接丢弃
4. **水平滚动**：命令超过 `detailWidth`（终端宽度 - 4）时，`tea.Tick(80ms)` 驱动水平滚动，每次 2 字符
5. **布局稳定**：详情行始终占一行（空或填充），不会导致列表位移

## tmux 配置

内嵌在 `config.go` 中作为常量字符串，每次启动覆盖写入 `~/.config/zed-tmux/tmux.conf`。

```bash
# generated by zed-tmux - do not edit
# this file is overwritten on every zed-tmux startup

set -g status off                                    # 隐藏状态栏，视觉上和普通终端一致
set -g default-terminal "tmux-256color"              # 终端类型
set -ag terminal-overrides ",xterm-256color:RGB"     # 真彩色支持
unbind s                                             # 禁用 session 切换（防御性，-L 已隔离）
unbind (
unbind )
unbind L
unbind $
set -g mouse on                                      # 鼠标支持
set -g base-index 1                                  # window 从 1 开始编号
setw -g pane-base-index 1                            # pane 从 1 开始编号
set -sg escape-time 10                               # 减少 escape 延迟（vim 用户）
set -g history-limit 50000                           # 增大 scrollback
```

如果写入失败（权限等），打印提示后 exec `$SHELL` 降级为普通终端。配置文件是整体架构的一部分（隐藏状态栏、禁用 session 切换等），缺失会导致 tmux 行为不符合预期，因此不降级运行。

## Session 生命周期

### 创建

```
tmux -L <socket> -f <config> new-session -s <name> -c <cwd>
```

### Attach

```
tmux -L <socket> -f <config> attach-session -t <name>
```

### 销毁

| 场景 | 触发方式 |
|---|---|
| TUI 中按 `d` 删除 | `tmux -L <socket> kill-session -t <name>` |
| shell 中输入 `exit` 关闭所有 window | tmux 自然行为：最后一个 window 关闭 → session 结束 |
| `zed-tmux gc` | 清理所有项目中 idle 超过阈值的 session |
| `zed-tmux kill-all` | `tmux -L <socket> kill-server`，清理当前项目所有 session 并停止 server |

### 保留

以下场景 session 保留（tmux 天然行为，不需要额外代码）：

| 场景 | 原因 |
|---|---|
| Zed 关闭/退出 | tmux server 是独立进程，不受影响 |
| Zed 崩溃 | 同上 |
| SSH 断连 | 同上 |
| 笔记本合盖 | 同上 |

## CLI 命令

### `zed-tmux`（无参数，默认行为）

TUI 选择器流程。由 rc 守卫自动调用，通常不需要手动运行。

### `zed-tmux list`

非交互式列出所有 `zed-*` socket 下的 session。遍历 `/tmp/tmux-<uid>/` 下所有 `zed-` 前缀的 socket 文件。

```
$ zed-tmux list
zed-501ebcd2:
  1           zsh           idle 3h
  2           node          idle 5min  attached

zed-a1b2c3d4:
  dev-server  python        idle 2d
```

### `zed-tmux gc [--dry-run] [--max-idle <duration>]`

清理所有项目中 idle 超过阈值的未 attach session。

- `--dry-run`：只打印不执行
- `--max-idle`：默认 `7d`，支持 Go duration 格式（`24h`、`168h`）和扩展格式（`7d`、`30d`、`1w`）

```
$ zed-tmux gc --dry-run --max-idle 30d
[dry-run] would kill zed-501ebcd2/1 (idle 45d)

$ zed-tmux gc --max-idle 30d
killed zed-501ebcd2/1 (idle 45d)
```

### `zed-tmux kill-all`

清理当前项目（`$PWD` 对应的 socket）的所有 session。执行 `tmux -L <socket> kill-server`，同时停止该 socket 的 tmux server 进程。

```
$ zed-tmux kill-all
killed all sessions on zed-501ebcd2

$ zed-tmux kill-all    # 再次执行
no sessions to kill
```

### `zed-tmux version`

```
$ zed-tmux version
zed-tmux 0.1.0
```

## Shell 集成

rc 文件（`.zshrc` / `.bashrc`，本地和远端部署同一份）：

```bash
# zed-tmux: Zed 终端自动进入 tmux 持久化 session
if [[ -n "$ZED_TERM" && -z "$TMUX" && -z "$ZED_TMUX_GUARD" ]]; then
    exec ~/.local/bin/zed-tmux
fi
```

- `$ZED_TERM`：Zed 对所有终端（本地 + 远程）注入 `ZED_TERM=true`（`crates/terminal/src/terminal.rs` 的 `insert_zed_terminal_env`）
- `$TMUX`：tmux 内部 shell 会设置此变量，防止嵌套
- `$ZED_TMUX_GUARD`：zed-tmux 降级为普通 shell 时设置，防止 rc 守卫再次触发导致死循环
- `exec`：替换 shell 进程为 Go 二进制，不留中间进程
- 普通 SSH 登录没有 `$ZED_TERM`，不受影响
- 放在 rc 文件**末尾**，确保其他初始化（PATH、alias 等）先完成

## 降级行为

| 条件 | 行为 |
|---|---|
| `$ZED_TERM` 为空或 `$TMUX` 非空 | `exit(0)`，rc 守卫已拦截，通常不会走到这里 |
| tmux 不在 PATH | 降级 banner + exec `$SHELL` |
| tmux 配置写入失败 | 降级 banner + exec `$SHELL`（配置是架构必需，不降级运行） |
| `listSessions` 失败 | 降级 banner + exec `$SHELL` |
| stdin 不是 TTY | 静默 exec `$SHELL`（无人在看终端，不打印 banner） |
| TUI 中按 `q` / `esc` | `exit(0)`，Zed 关闭该终端 tab |

降级时打印醒目 banner（TTY 下 bold yellow），告知用户当前是普通 shell 而非 tmux：

```
================================
  Degraded to plain shell
  Reason: tmux not found
  No session persistence
================================
```

分隔线宽度自适应内容。没有看到 banner 的终端就是 tmux session。

exec `$SHELL` 降级确保用户始终能拿到一个可用的终端，不会因为 zed-tmux 的问题导致终端 tab 打开即关闭。

## 实现注意事项

### macOS 平台特殊处理

1. **tmux socket 目录**：macOS 上 tmux 使用 `/private/tmp/tmux-<uid>/`（`/tmp` 是 symlink），不是 `$TMPDIR`（`/var/folders/...`）。`tmuxSocketDir()` 硬编码 `/tmp` 而非 `os.TempDir()`
2. **tmux 错误信息**：macOS 上 tmux 无 server 时报 `error connecting to ... (No such file or directory)`，Linux 上报 `no server running on ...`。代码同时匹配两种
3. **TTY 检测**：`/dev/null` 在 macOS 上也是 CharDevice，不能用 `stat.Mode() & ModeCharDevice` 判断。使用 `golang.org/x/term.IsTerminal()`（ioctl TCGETS/TIOCGETA）

### tmux 命令超时

`listSessions` 使用 `context.WithTimeout(5s)` + `exec.CommandContext`。防止残留 socket 文件（server 已死但 socket 未清理）导致 tmux 连接挂住。

### syscall.Exec 语义

`execTmux` 和 `execShell` 调用 `syscall.Exec`，当前 Go 进程被目标进程替换，不返回。这意味着：

- 没有中间进程，Zed 看到的 PID 直接是 tmux client 或 shell
- tmux client 退出时（detach 或 session 结束），Zed 看到进程退出，关闭 tab
- 如果 `syscall.Exec` 失败（极罕见），打印错误并 `exit(1)`

## 设计决策记录

| # | 问题 | 决策 | 理由 |
|---|---|---|---|
| 1 | session 命名 | 默认递增序号，可自定义，可按 r 重命名 | 简洁，socket 已隔离项目 |
| 2 | gc 默认阈值 | 7d | 覆盖周末断连场景，支持 `--max-idle 30d` |
| 3 | TUI 显示内容 | 列表显示命令名 + idle，详情预留行异步显示完整命令行（ps 查询），超长滚动 | 列表简洁不阻塞，详情按需加载零感知延迟 |
| 4 | q 退出行为 | 直接关闭 tab，无确认 | 启动阶段无工作状态，无损失 |
| 5 | 配置文件策略 | 每次覆盖 | 保证配置与代码一致，无需版本管理 |
| 6 | kill-all | 提供 | 项目结束或重置时一键清理 |
| 7 | tmux 不存在 | 打印提示 + exec $SHELL | 用户知道原因，终端仍可用 |
| 8 | 无 TTY | exec $SHELL | tmux 需要 TTY，无法绕过 |

## 文件结构

```
zed-tmux/
├── README.md           # 英文用户文档
├── README_cn.md        # 中文用户文档
├── LICENSE             # MIT
├── docs/
│   ├── DESIGN.md       # 设计文档（英文）
│   └── DESIGN_cn.md    # 本文档（中文）
├── go.mod              # Go 模块定义（module zed-tmux, go 1.25）
├── go.sum              # 依赖校验
├── main.go             # 入口 + CLI 分发 + 降级逻辑
├── config.go           # tmux 配置常量 + ensureConfig()
├── session.go          # tmux session 操作 + socket 计算 + 工具函数
├── tui.go              # bubbletea TUI 选择器（三模式状态机）
└── gc.go               # gc 清理 + duration 解析
```

### 各文件职责

**main.go** — 程序入口和 CLI 命令分发。

- `main()`：根据 `os.Args[1]` 分发到 `gc` / `list` / `kill-all` / `version`，无参数走 `runDefault()`
- `runDefault()`：完整的 TUI 流程——环境检查 → tmux 查找 → 配置生成 → session 查询 → TTY 检查 → TUI → exec tmux
- `runList()`：遍历所有 `zed-*` socket，打印 session 列表
- `runKillAll()`：对当前项目的 socket 执行 `tmux kill-server`
- `execTmux()`：`syscall.Exec` 进 tmux，Go 进程被替换，不返回
- `execShell()`：`syscall.Exec` 进 `$SHELL`，降级用
- `isTTY()`：`term.IsTerminal(int(os.Stdin.Fd()))`，ioctl 检测

**config.go** — tmux 配置管理。

- `tmuxConfigContent`：内嵌的 tmux 配置常量字符串
- `ensureConfig()`：`MkdirAll` + `WriteFile` 到 `~/.config/zed-tmux/tmux.conf`，每次覆盖，返回配置文件路径

**session.go** — tmux session 数据模型和操作。

- `Session` 结构体：Name, Attached, Windows, CurrentCommand, CurrentPath, TTY, Activity
- `socketName(cwd)`：sha256 前 4 字节 → `zed-<8hex>`
- `listSessions(socket)`：执行 `tmux -L <socket> list-sessions -F <format>`，5 秒 context 超时，解析 TSV 输出。无 server 时（`no server running` 或 `error connecting`）返回空列表而非错误
- `nextSessionName()`：纯数字名称取 max+1
- `killSession()` / `renameSession()`：调用 tmux 命令
- `validSessionName()`：校验名称（非空，不含 `.` `:`）
- `findZedSockets()`：扫描 `/tmp/tmux-<uid>/` 下 `zed-` 前缀的 socket 文件
- `tmuxSocketDir()`：`$TMUX_TMPDIR` 或 `/tmp` + `tmux-<uid>`（注意：macOS 上 tmux 用 `/tmp` 即 `/private/tmp`，不是 `$TMPDIR`）
- `formatIdle()`：`just now` / `5m` / `3h` / `2d`

**tui.go** — bubbletea TUI 选择器。

- 三个模式：`modeNormal`（列表导航）、`modeInput`（文本输入）、`modeConfirm`（删除确认）
- `model.sessions` 持有全量 session 列表，已 attach 的 session 显示但不可选择（光标跳过）
- 详情预留行：光标移动时通过 `pane_tty` + `ps -t` 异步查询完整命令行，generation 计数器丢弃过期结果
- `fetchDetail(tty)`：过滤 shell 进程，返回第一个非 shell 的完整命令行
- 超长命令在预留行内水平滚动（`scrollTickCmd`，80ms/tick，2 字符/tick）
- `runTUI()`：创建 `tea.Program`（启用 `WithMouseCellMotion`）并运行，返回用户的 `action`（Attach / Create / Quit）
- 支持鼠标点击选择 session（attached 行不可点击）
- 重命名和删除在 TUI 内部完成（调用 tmux → `refreshSessions()`），不退出 TUI
- 样式：选中行蓝色背景全宽高亮、Faint（路径、idle、帮助栏、attached 行）、青色（详情行 `⌘` 前缀）、黄色（`[attached]` 标记）、红色（错误信息）

**gc.go** — 垃圾回收。

- `runGC()`：解析 `--dry-run` / `--max-idle` 参数，遍历所有 `zed-*` socket，清理 idle 且未 attach 的 session
- `parseDuration()`：支持 Go 标准格式（`24h`）和扩展格式（`7d`、`1w`）
