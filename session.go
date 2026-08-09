package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Session struct {
	Name           string
	Attached       int
	Windows        int
	CurrentCommand string
	CurrentPath    string
	TTY            string
	Activity       time.Time
}

func (s Session) Idle() time.Duration {
	return time.Since(s.Activity)
}

func socketName(cwd string) string {
	hash := sha256.Sum256([]byte(cwd))
	return fmt.Sprintf("zed-%x", hash[:4])
}

func listSessions(socket string) ([]Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "-L", socket, "list-sessions",
		"-F", "#{session_name}\t#{session_attached}\t#{session_windows}\t#{pane_current_command}\t#{session_activity}\t#{pane_current_path}\t#{pane_tty}")
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("list sessions: timeout connecting to %s", socket)
	}
	if err != nil {
		msg := string(output)
		if strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting") {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %s", strings.TrimSpace(msg))
	}

	var sessions []Session
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 5 {
			continue
		}
		attached, err := strconv.Atoi(fields[1])
		if err != nil {
			attached = 0
		}
		windows, err := strconv.Atoi(fields[2])
		if err != nil {
			windows = 1
		}
		activityUnix, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			activityUnix = time.Now().Unix()
		}
		currentPath := ""
		if len(fields) > 5 {
			currentPath = fields[5]
		}
		tty := ""
		if len(fields) > 6 {
			tty = fields[6]
		}
		sessions = append(sessions, Session{
			Name:           fields[0],
			Attached:       attached,
			Windows:        windows,
			CurrentCommand: fields[3],
			CurrentPath:    currentPath,
			TTY:            tty,
			Activity:       time.Unix(activityUnix, 0),
		})
	}
	return sessions, nil
}

func nextSessionName(sessions []Session) string {
	maxNum := 0
	for _, s := range sessions {
		if n, err := strconv.Atoi(s.Name); err == nil && n > maxNum {
			maxNum = n
		}
	}
	return strconv.Itoa(maxNum + 1)
}

func killSession(socket, name string) error {
	cmd := exec.Command("tmux", "-L", socket, "kill-session", "-t", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kill session %s: %s", name, strings.TrimSpace(string(output)))
	}
	return nil
}

func renameSession(socket, oldName, newName string) error {
	cmd := exec.Command("tmux", "-L", socket, "rename-session", "-t", oldName, newName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rename session %s → %s: %s", oldName, newName, strings.TrimSpace(string(output)))
	}
	return nil
}

func validSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if strings.ContainsAny(name, ".:") {
		return fmt.Errorf("name cannot contain '.' or ':'")
	}
	return nil
}

func findZedSockets() ([]string, error) {
	dir := tmuxSocketDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read socket dir: %w", err)
	}
	var sockets []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "zed-") {
			sockets = append(sockets, e.Name())
		}
	}
	return sockets, nil
}

func tmuxSocketDir() string {
	if d := os.Getenv("TMUX_TMPDIR"); d != "" {
		return filepath.Join(d, fmt.Sprintf("tmux-%d", os.Getuid()))
	}
	return filepath.Join("/tmp", fmt.Sprintf("tmux-%d", os.Getuid()))
}

func formatIdle(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
