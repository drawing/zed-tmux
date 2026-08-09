package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/term"
)

const version = "0.1.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "gc":
			runGC(os.Args[2:])
		case "list":
			runList()
		case "kill-all":
			runKillAll()
		case "version":
			fmt.Printf("zed-tmux %s\n", version)
		default:
			fmt.Fprintf(os.Stderr, "zed-tmux: unknown command: %s\n", os.Args[1])
			os.Exit(1)
		}
		return
	}

	runDefault()
}

func runDefault() {
	if os.Getenv("ZED_TERM") == "" || os.Getenv("TMUX") != "" {
		os.Exit(0)
	}

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		execShell("tmux not found")
	}

	configPath, err := ensureConfig()
	if err != nil {
		execShell(fmt.Sprintf("config write failed: %v", err))
	}

	cwd, err := os.Getwd()
	if err != nil {
		execShell(fmt.Sprintf("cannot get working directory: %v", err))
	}

	socket := socketName(cwd)

	allSessions, err := listSessions(socket)
	if err != nil {
		execShell(fmt.Sprintf("cannot list sessions: %v", err))
	}

	if !isTTY() {
		execShell("")
	}

	act, err := runTUI(allSessions, socket, cwd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: %v\n", err)
		os.Exit(1)
	}

	switch act.typ {
	case actionAttach:
		execTmux(tmuxPath, configPath, socket, "attach-session", "-t", act.session)
	case actionCreate:
		execTmux(tmuxPath, configPath, socket, "new-session", "-s", act.session, "-c", cwd)
	case actionQuit:
		os.Exit(0)
	}
}

func runList() {
	sockets, err := findZedSockets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: %v\n", err)
		os.Exit(1)
	}
	if len(sockets) == 0 {
		fmt.Println("no zed-tmux sessions")
		return
	}
	found := false
	for _, socket := range sockets {
		sessions, err := listSessions(socket)
		if err != nil || len(sessions) == 0 {
			continue
		}
		found = true
		fmt.Printf("%s:\n", socket)
		for _, s := range sessions {
			attached := ""
			if s.Attached > 0 {
				attached = "  attached"
			}
			windows := ""
			if s.Windows > 1 {
				windows = fmt.Sprintf("  %dw", s.Windows)
			}
			cmd := s.CurrentCommand
			if cmd == "" {
				cmd = "?"
			}
			fmt.Printf("  %-10s  %-12s%s  idle %s%s\n",
				s.Name, cmd, windows, formatIdle(s.Idle()), attached)
		}
		fmt.Println()
	}
	if !found {
		fmt.Println("no zed-tmux sessions")
	}
}

func runKillAll() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: %v\n", err)
		os.Exit(1)
	}
	socket := socketName(cwd)
	cmd := exec.Command("tmux", "-L", socket, "kill-server")
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(output)
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting") {
			fmt.Println("no sessions to kill")
			return
		}
		fmt.Fprintf(os.Stderr, "zed-tmux: kill-server: %s\n", strings.TrimSpace(msg))
		os.Exit(1)
	}
	fmt.Printf("killed all sessions on %s\n", socket)
}

func execTmux(tmuxPath, configPath, socket string, args ...string) {
	fullArgs := []string{"tmux", "-L", socket}
	if configPath != "" {
		fullArgs = append(fullArgs, "-f", configPath)
	}
	fullArgs = append(fullArgs, args...)
	err := syscall.Exec(tmuxPath, fullArgs, os.Environ())
	fmt.Fprintf(os.Stderr, "zed-tmux: exec tmux failed: %v\n", err)
	os.Exit(1)
}

func execShell(reason string) {
	if reason != "" {
		printBanner(reason)
	}
	os.Setenv("ZED_TMUX_GUARD", "1")
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	err := syscall.Exec(shell, []string{shell}, os.Environ())
	fmt.Fprintf(os.Stderr, "zed-tmux: exec shell failed: %v\n", err)
	os.Exit(1)
}

func printBanner(reason string) {
	lines := []string{
		"  Degraded to plain shell",
		"  Reason: " + reason,
		"  No session persistence",
	}
	width := 0
	for _, line := range lines {
		if w := displayWidth(line); w > width {
			width = w
		}
	}
	sep := strings.Repeat("=", width+2)

	colorize := isTTY()
	if colorize {
		fmt.Printf("\033[1;33m%s\033[0m\n", sep)
		for _, line := range lines {
			fmt.Printf("\033[1;33m%s\033[0m\n", line)
		}
		fmt.Printf("\033[1;33m%s\033[0m\n", sep)
	} else {
		fmt.Println(sep)
		for _, line := range lines {
			fmt.Println(line)
		}
		fmt.Println(sep)
	}
}

func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if r > 0x7F {
			w += 2
		} else {
			w++
		}
	}
	return w
}

func isTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
