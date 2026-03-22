package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionData_JSONRoundTrip(t *testing.T) {
	original := &SessionData{
		Version:   1,
		ActiveTab: 1,
		Tabs: []TabData{
			{
				Cwd:         "/home/user",
				Title:       "dev",
				UserRenamed: true,
				PinnedSlot:  "a",
				Note:        "working on feature X",
				Layout: &PaneLayout{
					Kind:  "hsplit",
					Ratio: 0.6,
					Left: &PaneLayout{
						Kind: "leaf",
						Cwd:  "/home/user/project",
					},
					Right: &PaneLayout{
						Kind:  "vsplit",
						Ratio: 0.5,
						Left: &PaneLayout{
							Kind:       "leaf",
							Cwd:        "/tmp",
							CustomName: "logs",
						},
						Right: &PaneLayout{
							Kind:            "leaf",
							ServerSessionID: "abc123",
						},
					},
				},
			},
			{
				Cwd:   "/var/log",
				Title: "logs",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored SessionData
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if restored.Version != 1 {
		t.Errorf("Version = %d, want 1", restored.Version)
	}
	if restored.ActiveTab != 1 {
		t.Errorf("ActiveTab = %d, want 1", restored.ActiveTab)
	}
	if len(restored.Tabs) != 2 {
		t.Fatalf("len(Tabs) = %d, want 2", len(restored.Tabs))
	}

	tab0 := restored.Tabs[0]
	if tab0.Cwd != "/home/user" {
		t.Errorf("tab0.Cwd = %q", tab0.Cwd)
	}
	if !tab0.UserRenamed {
		t.Error("tab0.UserRenamed should be true")
	}
	if tab0.PinnedSlot != "a" {
		t.Errorf("tab0.PinnedSlot = %q, want 'a'", tab0.PinnedSlot)
	}
	if tab0.Note != "working on feature X" {
		t.Errorf("tab0.Note = %q", tab0.Note)
	}
	if tab0.Layout == nil {
		t.Fatal("tab0.Layout is nil")
	}
	if tab0.Layout.Kind != "hsplit" {
		t.Errorf("tab0.Layout.Kind = %q, want 'hsplit'", tab0.Layout.Kind)
	}
	if tab0.Layout.Ratio != 0.6 {
		t.Errorf("tab0.Layout.Ratio = %f, want 0.6", tab0.Layout.Ratio)
	}
	if tab0.Layout.Right == nil || tab0.Layout.Right.Right == nil {
		t.Fatal("nested layout is nil")
	}
	if tab0.Layout.Right.Right.ServerSessionID != "abc123" {
		t.Errorf("ServerSessionID = %q, want 'abc123'", tab0.Layout.Right.Right.ServerSessionID)
	}
}

func TestSessionData_CorruptJSON(t *testing.T) {
	var data SessionData
	err := json.Unmarshal([]byte(`{invalid json`), &data)
	if err == nil {
		t.Error("expected error for corrupt JSON")
	}
}

func TestSessionData_EmptyTabs(t *testing.T) {
	original := &SessionData{Version: 1, ActiveTab: 0, Tabs: nil}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var restored SessionData
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if restored.Tabs != nil {
		t.Errorf("expected nil Tabs, got %d", len(restored.Tabs))
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")

	original := &SessionData{
		Version:   1,
		ActiveTab: 0,
		Tabs: []TabData{
			{Cwd: "/tmp", Title: "test"},
		},
	}

	// Write directly to test path (bypassing Path() which uses UserHomeDir).
	data, err := json.MarshalIndent(original, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Read it back.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	var restored SessionData
	if err := json.NewDecoder(f).Decode(&restored); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if len(restored.Tabs) != 1 || restored.Tabs[0].Cwd != "/tmp" {
		t.Errorf("round-trip failed: %+v", restored)
	}
}

func TestPaneLayout_OmitEmpty(t *testing.T) {
	leaf := PaneLayout{Kind: "leaf", Cwd: "/tmp"}
	data, err := json.Marshal(leaf)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	s := string(data)
	// Leaf should not have "left", "right", "ratio", "custom_name", "server_session_id" keys.
	for _, key := range []string{"left", "right", "ratio", "custom_name", "server_session_id"} {
		if contains(s, `"`+key+`"`) {
			t.Errorf("leaf JSON should not contain %q: %s", key, s)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
