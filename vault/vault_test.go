package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSuggestPrefixMatch(t *testing.T) {
	v := &Vault{index: make(map[string]struct{})}
	v.commands = []string{
		"git commit -m 'fix bug'",
		"git push origin main",
		"docker compose up -d",
		"ls -la",
	}
	for _, cmd := range v.commands {
		v.index[cmd] = struct{}{}
	}

	tests := []struct {
		line string
		want string
	}{
		// Line ends with "git comm" which is a prefix of "git commit -m 'fix bug'"
		{"user@host:~$ git comm", "it -m 'fix bug'"},
		// Full match — no suggestion (already typed)
		{"$ ls -la", ""},
		// Line ends with "docker com" → prefix of "docker compose up -d"
		{"% docker com", "pose up -d"},
		// Too short input
		{"$ g", ""},
		// No match
		{"$ xyz", ""},
		// Empty line
		{"", ""},
	}

	for _, tt := range tests {
		got := v.Suggest(tt.line, 0)
		if got != tt.want {
			t.Errorf("Suggest(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestSuggestMostRecent(t *testing.T) {
	v := &Vault{index: make(map[string]struct{})}
	v.commands = []string{
		"git commit -m 'old'",
		"git commit -m 'new'",
	}
	for _, cmd := range v.commands {
		v.index[cmd] = struct{}{}
	}

	// Should match most recent (last in slice).
	got := v.Suggest("$ git commit", 0)
	if got != " -m 'new'" {
		t.Errorf("Suggest() = %q, want %q", got, " -m 'new'")
	}
}

func TestSuggestSkipCycle(t *testing.T) {
	v := &Vault{index: make(map[string]struct{})}
	v.commands = []string{
		"git commit -m 'first'",
		"git commit -m 'second'",
		"git commit -m 'third'",
	}
	for _, cmd := range v.commands {
		v.index[cmd] = struct{}{}
	}

	// skip=0 → most recent (third)
	got := v.Suggest("$ git commit", 0)
	if got != " -m 'third'" {
		t.Errorf("skip=0: got %q, want %q", got, " -m 'third'")
	}

	// skip=1 → second most recent
	got = v.Suggest("$ git commit", 1)
	if got != " -m 'second'" {
		t.Errorf("skip=1: got %q, want %q", got, " -m 'second'")
	}

	// skip=2 → oldest
	got = v.Suggest("$ git commit", 2)
	if got != " -m 'first'" {
		t.Errorf("skip=2: got %q, want %q", got, " -m 'first'")
	}

	// skip=3 → no more matches, returns empty (caller wraps around)
	got = v.Suggest("$ git commit", 3)
	if got != "" {
		t.Errorf("skip=3: got %q, want empty", got)
	}
}

func TestAddDedup(t *testing.T) {
	v := &Vault{index: make(map[string]struct{})}
	v.Add("ls -la")
	v.Add("pwd")
	v.Add("ls -la") // duplicate — should move to end

	if len(v.commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(v.commands))
	}
	if v.commands[0] != "pwd" {
		t.Errorf("commands[0] = %q, want %q", v.commands[0], "pwd")
	}
	if v.commands[1] != "ls -la" {
		t.Errorf("commands[1] = %q, want %q", v.commands[1], "ls -la")
	}
}

func TestAddIgnorePrefix(t *testing.T) {
	v := &Vault{index: make(map[string]struct{}), ignorePfx: " "}
	v.Add(" secret-command")
	v.Add("normal-command")

	if len(v.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(v.commands))
	}
	if v.commands[0] != "normal-command" {
		t.Errorf("commands[0] = %q, want %q", v.commands[0], "normal-command")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "test.key")

	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		t.Fatalf("loadOrCreateKey: %v", err)
	}

	plaintext := []byte("git commit -m 'test'\nls -la\npwd")
	enc, err := encrypt(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	dec, err := decrypt(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}

	if string(dec) != string(plaintext) {
		t.Errorf("roundtrip failed: got %q, want %q", string(dec), string(plaintext))
	}
}

func TestLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	v := New(dir, " ")
	v.Add("ls -la")
	v.Add("git status")
	v.Add(" secret") // should be ignored

	if err := v.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	v2 := New(dir, " ")
	if err := v2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if v2.Len() != 2 {
		t.Fatalf("expected 2 commands after load, got %d", v2.Len())
	}
}

func TestParseZshHistory(t *testing.T) {
	dir := t.TempDir()
	histFile := filepath.Join(dir, ".zsh_history")

	content := `: 1234567890:0;git commit -m 'test'
: 1234567891:0;ls -la
plain command here
: 1234567892:0;git commit -m 'test'
`
	if err := os.WriteFile(histFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write test history: %v", err)
	}

	commands, err := ParseZshHistory(histFile)
	if err != nil {
		t.Fatalf("ParseZshHistory: %v", err)
	}

	// Should have 3 unique commands (git commit appears twice).
	if len(commands) != 3 {
		t.Fatalf("expected 3 commands, got %d: %v", len(commands), commands)
	}
	if commands[0] != "git commit -m 'test'" {
		t.Errorf("commands[0] = %q", commands[0])
	}
	if commands[1] != "ls -la" {
		t.Errorf("commands[1] = %q", commands[1])
	}
	if commands[2] != "plain command here" {
		t.Errorf("commands[2] = %q", commands[2])
	}
}
