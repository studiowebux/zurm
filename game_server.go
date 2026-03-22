package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"text/tabwriter"

	"github.com/studiowebux/zurm/config"
	"github.com/studiowebux/zurm/pane"
	"github.com/studiowebux/zurm/renderer"
	"github.com/studiowebux/zurm/tab"
	"github.com/studiowebux/zurm/zserver"
)

func fetchSessions(addr string) ([]zserver.SessionInfo, error) {
	conn, err := net.Dial("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to zurm-server at %s: %w", addr, err)
	}
	defer conn.Close()

	if err := zserver.WriteMessage(conn, zserver.MsgListSessions, nil); err != nil {
		return nil, fmt.Errorf("send list request: %w", err)
	}

	msg, err := zserver.ReadMessage(conn)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if msg.Type != zserver.MsgSessionList {
		return nil, fmt.Errorf("unexpected response type 0x%02x", msg.Type)
	}

	var sessions []zserver.SessionInfo
	if len(msg.Payload) > 0 {
		if err := json.Unmarshal(msg.Payload, &sessions); err != nil {
			return nil, fmt.Errorf("decode session list: %w", err)
		}
	}
	return sessions, nil
}

// killSession connects to zurm-server and kills the session with the given ID.
func killSession(addr, id string) error {
	conn, err := net.Dial("unix", addr)
	if err != nil {
		return fmt.Errorf("cannot connect to zurm-server at %s: %w", addr, err)
	}
	defer conn.Close()
	data, err := json.Marshal(zserver.KillSessionRequest{ID: id})
	if err != nil {
		return fmt.Errorf("marshal kill request: %w", err)
	}
	return zserver.WriteMessage(conn, zserver.MsgKillSession, data)
}

// resolveSessionPrefix matches a short prefix (like Docker short IDs) against
// active server sessions. Returns the full ID or an error if zero or multiple
// sessions match.
func resolveSessionPrefix(addr, prefix string) (string, error) {
	sessions, err := fetchSessions(addr)
	if err != nil {
		return "", err
	}
	var matches []string
	for _, s := range sessions {
		if len(s.ID) >= len(prefix) && s.ID[:len(prefix)] == prefix {
			matches = append(matches, s.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no session matching prefix %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous prefix %q matches %d sessions: %v", prefix, len(matches), matches)
	}
}

// runListSessions connects to zurm-server, fetches the session list, prints a
// table to stdout, and returns. Called by the --list-sessions / -ls flag before
// the GUI starts.
func runListSessions() error {
	cfg, err := config.Load()
	if err != nil {
		log.Printf("config load warning: %v (using defaults)", err)
	}

	addr := zserver.ResolveSocket(cfg.Server.Address)
	sessions, err := fetchSessions(addr)
	if err != nil {
		return err
	}

	if len(sessions) == 0 {
		fmt.Println("No active server sessions.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tPID\tSIZE\tDIR")
	for _, s := range sessions {
		fmt.Fprintf(w, "%s\t%s\t%d\t%dx%d\t%s\n", s.ID, s.Name, s.PID, s.Cols, s.Rows, s.Dir)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush session list: %w", err)
	}
	return nil
}

// attachServerSession connects to zurm-server, lists active sessions, and
// populates the command palette with an entry per session. Selecting an entry
// opens a new server-backed tab attached to that session.
// Called from the "Attach to Server Session" palette action.
func (g *Game) attachServerSession() {
	addr := zserver.ResolveSocket(g.cfg.Server.Address)
	sessions, err := fetchSessions(addr)
	if err != nil {
		g.flashStatus("zurm-server unreachable")
		return
	}

	if len(sessions) == 0 {
		g.flashStatus("No active server sessions")
		return
	}

	// Rebuild palette from scratch to prevent duplicate entries from
	// previous attach calls, then append per-session entries.
	g.buildPalette()

	for _, s := range sessions {
		si := s // capture for closure
		displayName := si.ID
		if si.Name != "" {
			displayName = si.Name
		} else if len(si.ID) > 8 {
			displayName = si.ID[:8]
		}
		attachLabel := fmt.Sprintf("Attach: %s (pid %d, %dx%d, %s)", displayName, si.PID, si.Cols, si.Rows, si.Dir)
		g.palette.Entries = append(g.palette.Entries, renderer.PaletteEntry{Name: attachLabel})
		g.palette.Actions = append(g.palette.Actions, func() {
			g.openServerTabForSession(si.ID)
		})

		killLabel := fmt.Sprintf("Kill: %s (pid %d)", displayName, si.PID)
		g.palette.Entries = append(g.palette.Entries, renderer.PaletteEntry{Name: killLabel})
		g.palette.Actions = append(g.palette.Actions, func() {
			if err := killSession(addr, si.ID); err != nil {
				g.flashStatus("Kill failed: " + err.Error())
			} else {
				g.flashStatus("Killed session " + displayName)
			}
		})
	}

	// Open palette pre-filtered to show server entries.
	g.openPalette()
	g.palette.State.Query = "Attach"
}

// openServerTabForSession opens a new tab backed by an existing zurm-server
// session identified by sessionID.
func (g *Game) openServerTabForSession(sessionID string) {
	paneRect := g.contentRect()

	p, err := pane.NewServer(g.cfg, paneRect, g.font.CellW, g.font.CellH, "", sessionID)
	if err != nil {
		g.flashStatus("Attach failed: " + err.Error())
		return
	}
	layout := pane.NewLeaf(p)
	layout.ComputeRects(paneRect, g.font.CellW, g.font.CellH, g.cfg.Window.Padding, g.cfg.Panes.DividerWidthPixels)
	for _, leaf := range layout.Leaves() {
		leaf.Pane.Term.Resize(leaf.Pane.Cols, leaf.Pane.Rows)
	}
	t := &tab.Tab{
		Layout:  layout,
		Focused: p,
		Title:   fmt.Sprintf("tab %d", len(g.tabMgr.Tabs)+1),
	}
	g.tabMgr.Tabs = append(g.tabMgr.Tabs, t)
	g.switchTab(len(g.tabMgr.Tabs) - 1)
}
