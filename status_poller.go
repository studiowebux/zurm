package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	gitInfoTimeout  = 5 * time.Second // max wait for git status subprocess
	cwdPollInterval = 2 * time.Second // how often to query CWD via lsof/OSC 7
	fgPollInterval  = 1 * time.Second // how often to query foreground process via ps
)

// gitInfo holds all git status data gathered asynchronously.
type gitInfo struct {
	Branch string
	Commit string
	Dirty  int
	Staged int
	Ahead  int
	Behind int
}

// gitInfoResult pairs a query generation counter with the git status result.
// DrainGit discards results whose gen no longer matches the current gen,
// preventing stale goroutines from overwriting a newer query's output.
type gitInfoResult struct {
	gen  uint64
	info gitInfo
}

// StatusPoller manages asynchronous git status queries and output-driven
// CWD/foreground polling intervals. It does not know about rendering —
// the game loop reads results and applies them to status bar state.
type StatusPoller struct {
	gitCh     chan gitInfoResult
	gitGen    uint64
	gitCancel context.CancelFunc

	lastPollSeq uint64
	lastCwdPoll time.Time
	lastFgPoll  time.Time
}

// NewStatusPoller creates a poller with a buffered git result channel.
func NewStatusPoller() *StatusPoller {
	return &StatusPoller{
		gitCh: make(chan gitInfoResult, 1),
	}
}

// StartGitQuery cancels any in-flight git query, drains stale results,
// and launches a new goroutine to query git status for cwd.
func (sp *StatusPoller) StartGitQuery(cwd string) {
	if sp.gitCancel != nil {
		sp.gitCancel()
	}
	// Drain stale result.
	select {
	case <-sp.gitCh:
	default:
	}
	sp.gitGen++
	gen := sp.gitGen
	ctx, cancel := context.WithTimeout(context.Background(), gitInfoTimeout)
	sp.gitCancel = cancel
	ch := sp.gitCh

	go func() {
		defer cancel()
		info := gitInfo{}

		// Single call: branch + dirty/staged + ahead/behind.
		if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "status", "--porcelain", "-b").Output(); err == nil { // #nosec G204
			lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
			if len(lines) > 0 && strings.HasPrefix(lines[0], "## ") {
				header := lines[0][3:]
				if dotIdx := strings.Index(header, "..."); dotIdx >= 0 {
					info.Branch = header[:dotIdx]
					rest := header[dotIdx+3:]
					if brk := strings.Index(rest, " ["); brk >= 0 && strings.HasSuffix(rest, "]") {
						for _, part := range strings.Split(rest[brk+2:len(rest)-1], ", ") {
							fmt.Sscanf(part, "ahead %d", &info.Ahead)  //nolint:errcheck
							fmt.Sscanf(part, "behind %d", &info.Behind) //nolint:errcheck
						}
					}
				} else if header == "HEAD (no branch)" {
					info.Branch = "HEAD"
				} else {
					info.Branch = header
				}
				for _, line := range lines[1:] {
					if len(line) < 2 {
						continue
					}
					idx := line[0]
					wt := line[1]
					if idx != ' ' && idx != '?' {
						info.Staged++
					}
					if wt != ' ' && wt != '?' {
						info.Dirty++
					}
				}
			}
		} else {
			select {
			case ch <- gitInfoResult{gen: gen, info: info}:
			default:
			}
			return
		}

		// Short commit hash — no equivalent in porcelain output.
		if out, err := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--short", "HEAD").Output(); err == nil { // #nosec G204
			info.Commit = strings.TrimSpace(string(out))
		}

		select {
		case ch <- gitInfoResult{gen: gen, info: info}:
		default:
		}
	}()
}

// DrainGit returns the latest git info if a new result is available.
// Stale results (from superseded queries) are silently discarded.
func (sp *StatusPoller) DrainGit() (gitInfo, bool) {
	select {
	case res := <-sp.gitCh:
		if res.gen != sp.gitGen {
			return gitInfo{}, false // stale
		}
		return res.info, true
	default:
		return gitInfo{}, false
	}
}

// ShouldPollCwd returns true if enough time has passed since the last CWD poll
// and a new PTY render sequence has been observed. Updates internal timestamps.
func (sp *StatusPoller) ShouldPollCwd(renderSeq uint64) bool {
	if renderSeq == sp.lastPollSeq {
		return false
	}
	now := time.Now()
	if now.Sub(sp.lastCwdPoll) < cwdPollInterval {
		return false
	}
	sp.lastPollSeq = renderSeq
	sp.lastCwdPoll = now
	return true
}

// ShouldPollFg returns true if enough time has passed since the last
// foreground process poll. Updates internal timestamp.
func (sp *StatusPoller) ShouldPollFg(renderSeq uint64) bool {
	if renderSeq == sp.lastPollSeq {
		// Already checked by ShouldPollCwd — update seq.
	}
	sp.lastPollSeq = renderSeq
	now := time.Now()
	if now.Sub(sp.lastFgPoll) < fgPollInterval {
		return false
	}
	sp.lastFgPoll = now
	return true
}
