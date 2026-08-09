package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const guardMarker = "ZED_TMUX_GUARD"

func runInit() {
	shell := os.Getenv("SHELL")
	shellName := filepath.Base(shell)

	var rcPath string
	switch shellName {
	case "zsh":
		rcPath = filepath.Join(homeDir(), ".zshrc")
	case "bash":
		rcPath = filepath.Join(homeDir(), ".bashrc")
	default:
		fmt.Printf("Unsupported shell: %s\n", shellName)
		fmt.Println("Add the following to your shell rc file manually:")
		fmt.Println()
		printGuardBlock("/path/to/zed-tmux")
		os.Exit(1)
	}

	if content, err := os.ReadFile(rcPath); err == nil && strings.Contains(string(content), guardMarker) {
		fmt.Printf("Already configured: %s\n", rcPath)
		return
	}

	binPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: cannot determine executable path: %v\n", err)
		os.Exit(1)
	}
	binPath, err = filepath.EvalSymlinks(binPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: cannot resolve executable path: %v\n", err)
		os.Exit(1)
	}

	f, err := os.OpenFile(rcPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: cannot open %s: %v\n", rcPath, err)
		os.Exit(1)
	}
	defer f.Close()

	guard := fmt.Sprintf("\n# zed-tmux: persistent terminal sessions in Zed\nif [[ -n \"$ZED_TERM\" && -z \"$TMUX\" && -z \"$ZED_TMUX_GUARD\" ]]; then\n    exec %s\nfi\n", binPath)
	if _, err := f.WriteString(guard); err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: cannot write to %s: %v\n", rcPath, err)
		os.Exit(1)
	}

	fmt.Printf("Detected shell: %s\n", shellName)
	fmt.Printf("Added guard to %s\n", rcPath)
	fmt.Printf("\nDone. Restart your terminal or run: source %s\n", rcPath)
}

func printGuardBlock(binPath string) {
	fmt.Printf("# zed-tmux: persistent terminal sessions in Zed\n")
	fmt.Printf("if [[ -n \"$ZED_TERM\" && -z \"$TMUX\" && -z \"$ZED_TMUX_GUARD\" ]]; then\n")
	fmt.Printf("    exec %s\n", binPath)
	fmt.Printf("fi\n")
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: cannot get home directory: %v\n", err)
		os.Exit(1)
	}
	return home
}
