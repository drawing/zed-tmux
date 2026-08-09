# zed-tmux

基于 [tmux](https://github.com/tmux/tmux) 的 [Zed](https://zed.dev) 编辑器终端会话持久化工具。

[English](README.md)

## 为什么做这个

Zed 的终端 tab 生命周期绑定在编辑器进程上。关闭窗口、编辑器崩溃、SSH 断连——shell 进程直接死亡，长时间运行的任务（dev server、AI agent、编译）全部丢失。

zed-tmux 将每个 Zed 终端 tab 桥接到一个 tmux session。tmux server 进程独立于编辑器存活，会话在重启、崩溃、断连后都能恢复。

## 特性

- **全自动** — shell rc 文件只需 3 行守卫代码，Zed 终端启动时自动介入，无需手动操作
- **TUI 选择器** — 键盘或鼠标选择、新建、重命名、删除 session
- **按项目隔离** — 每个项目目录通过命名 socket 使用独立的 tmux server
- **详情行** — 光标移到某个 session 时异步查询完整命令行（`ps` 查询，超长命令水平滚动）
- **优雅降级** — tmux 缺失或任何环节出错时，回退到普通 shell 并显示醒目提示
- **零配置** — tmux 配置内嵌在二进制中，每次启动自动生成

## 环境要求

- [Zed](https://zed.dev) 编辑器
- [tmux](https://github.com/tmux/tmux) 3.0+
- [Go](https://go.dev) 1.21+（编译用）

## 安装

### 预编译二进制

从 [GitHub Releases](https://github.com/drawing/zed-tmux/releases) 下载对应平台的最新版本：

```bash
# 示例：macOS ARM64
curl -fsSL https://github.com/drawing/zed-tmux/releases/latest/download/zed-tmux_Darwin_arm64.tar.gz | tar xz
mv zed-tmux ~/.local/bin/
```

### 从源码构建

```bash
# 需要 Go 1.21+
go install github.com/drawing/zed-tmux@latest

# 或手动构建
git clone https://github.com/drawing/zed-tmux.git
cd zed-tmux
go build -o ~/.local/bin/zed-tmux .
```

### 交叉编译到远端服务器

```bash
GOOS=linux GOARCH=amd64 go build -o zed-tmux-linux .
scp zed-tmux-linux remote:~/.local/bin/zed-tmux
```

单文件静态二进制，除目标机器上的 tmux 外零运行时依赖。

## 配置（必须，仅需一次）

> [!IMPORTANT]
> 安装二进制后，**必须**完成以下配置。没有它 zed-tmux 不会激活。

### 自动配置（推荐）

```bash
zed-tmux init
```

自动检测 shell（`zsh` / `bash`），将守卫代码追加到对应的 rc 文件，并检查 `PATH`。在每台使用 Zed 的机器上执行一次（本地和远端）。

### 手工配置

**第 1 步** — 确认二进制在 `PATH` 中：

```bash
# 检查
which zed-tmux

# 如果找不到，在 .zshrc / .bashrc 中添加：
export PATH="$HOME/.local/bin:$PATH"
```

**第 2 步** — 在 `.zshrc` 或 `.bashrc` 的**末尾**添加（本地和远端都需要）：

```bash
# zed-tmux: Zed 终端自动进入 tmux 持久化 session
if [[ -n "$ZED_TERM" && -z "$TMUX" && -z "$ZED_TMUX_GUARD" ]]; then
    exec ~/.local/bin/zed-tmux
fi
```

> 如果通过 `go install` 或 Homebrew 安装，请将 `~/.local/bin/zed-tmux` 替换为 `which zed-tmux` 输出的实际路径。

### 守卫代码说明

| 变量 | 作用 |
|---|---|
| `$ZED_TERM` | Zed 对所有终端（本地 + 远程）注入此变量 |
| `$TMUX` | 已在 tmux 内时阻止嵌套 |
| `$ZED_TMUX_GUARD` | zed-tmux 降级为普通 shell 时设置，防止 rc 守卫再次触发 |

`exec` 替换 shell 进程为 zed-tmux，不留中间进程。非 Zed 终端（普通 SSH、iTerm、Terminal.app 等）不受影响。

## 使用

### TUI 选择器

在 Zed 中新建终端 tab 时自动弹出：

```
  zed-tmux · /Users/you/project

  ▸ 1           node          idle 5min
    2           zsh           idle 3h
    dev-server  python3       idle 2d  [attached]

  ⌘ node --expose-gc ~/.qwen/.../cli.js --config prod.yaml
  ↑↓/click select  enter attach  n new  r rename  d delete  q quit
```

| 按键 | 行为 |
|---|---|
| `↑` / `k` | 上移光标 |
| `↓` / `j` | 下移光标 |
| `enter` / 点击 | attach 到选中 session |
| `n` | 新建 session（预填下一个递增序号） |
| `r` | 重命名选中 session |
| `d` | 删除选中 session（二次确认） |
| `q` / `esc` | 退出（关闭终端 tab） |

已被其他终端 attach 的 session 整行 dim 显示并标黄色 `[attached]`，光标导航跳过，不可 attach、重命名或删除。

列表下方的**详情行**显示选中 session 的完整命令行，通过 `ps -t <pane_tty>` 异步查询。纯 shell session 详情行为空。命令超过终端宽度时水平滚动（marquee 效果）。

### CLI 命令

```bash
zed-tmux                                   # TUI 选择器（通常由 rc 守卫自动调用）
zed-tmux init                              # 一次性配置：自动添加 shell 守卫到 rc 文件
zed-tmux list                              # 列出所有项目的所有 session
zed-tmux gc [--dry-run] [--max-idle 30d]   # 清理空闲且未 attach 的 session
zed-tmux kill-all                          # 清理当前项目的所有 session
zed-tmux version                           # 打印版本号
```

`gc` 默认阈值 `--max-idle 7d`。时间格式支持 Go 标准格式（`24h`、`168h`）和扩展格式（`7d`、`1w`）。

## Session 生命周期

| 场景 | Session 是否保留 |
|---|---|
| Zed 关闭 / 退出 | ✅ 保留 |
| Zed 崩溃 | ✅ 保留 |
| SSH 断连 | ✅ 保留 |
| 笔记本合盖 | ✅ 保留 |
| 在 shell 中输入 `exit`（最后一个 window） | ❌ 自然结束 |
| TUI 中按 `d` 删除 | ❌ 删除 |
| `zed-tmux kill-all` | ❌ 同时停止 tmux server |
| `zed-tmux gc` | ❌ 仅清理空闲且未 attach 的 |

## 降级行为

任何环节出错时，zed-tmux 回退到普通 shell 并显示醒目 banner：

```
================================
  Degraded to plain shell
  Reason: tmux not found
  No session persistence
================================
```

没有看到 banner 的终端就是 tmux session。

| 条件 | 行为 |
|---|---|
| tmux 未安装 | Banner + 普通 shell |
| 配置文件写入失败 | Banner + 普通 shell |
| Session 查询失败 | Banner + 普通 shell |
| stdin 不是 TTY | 静默回退到普通 shell（无 banner，因为没有人在看） |
| TUI 中按 `q` / `esc` | `exit(0)`，Zed 关闭该 tab |

## 依赖

| 依赖 | 用途 |
|---|---|
| [bubbletea](https://github.com/charmbracelet/bubbletea) | TUI 框架（Elm 架构） |
| [lipgloss](https://github.com/charmbracelet/lipgloss) | TUI 样式（颜色、加粗、faint） |
| [bubbles](https://github.com/charmbracelet/bubbles) | 文本输入组件 |
| [x/term](https://golang.org/x/term) | TTY 检测（基于 ioctl） |

## 设计文档

架构、实现细节和设计决策见 [docs/DESIGN_cn.md](docs/DESIGN_cn.md)。

## 许可证

[MIT](LICENSE)
