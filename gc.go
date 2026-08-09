package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func runGC(args []string) {
	fs := flag.NewFlagSet("gc", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "only print, don't kill")
	maxIdle := fs.String("max-idle", "7d", "max idle duration (e.g. 24h, 7d, 30d, 1w)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: %v\n", err)
		os.Exit(1)
	}

	idle, err := parseDuration(*maxIdle)
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: %v\n", err)
		os.Exit(1)
	}

	sockets, err := findZedSockets()
	if err != nil {
		fmt.Fprintf(os.Stderr, "zed-tmux: %v\n", err)
		os.Exit(1)
	}

	killed := 0
	for _, socket := range sockets {
		sessions, err := listSessions(socket)
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if s.Attached > 0 || s.Idle() < idle {
				continue
			}
			if *dryRun {
				fmt.Printf("[dry-run] would kill %s/%s (idle %s)\n", socket, s.Name, formatIdle(s.Idle()))
			} else {
				if err := killSession(socket, s.Name); err != nil {
					fmt.Fprintf(os.Stderr, "zed-tmux: %v\n", err)
					continue
				}
				fmt.Printf("killed %s/%s (idle %s)\n", socket, s.Name, formatIdle(s.Idle()))
			}
			killed++
		}
	}

	if killed == 0 {
		fmt.Println("nothing to clean up")
	}
}

func parseDuration(s string) (time.Duration, error) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "w"))
		if err != nil {
			return 0, fmt.Errorf("invalid duration: %s", s)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("invalid duration: %s (use e.g. 24h, 7d, 1w)", s)
}
