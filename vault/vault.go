package vault

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Vault stores and retrieves encrypted command history for autosuggestion.
// Pattern: repository — single access point for persisted command data.
type Vault struct {
	mu       sync.RWMutex
	commands []string            // deduplicated, most recent last
	index    map[string]struct{} // O(1) dedup check
	dirty    bool

	vaultPath  string
	keyPath    string
	ignorePfx  string
	maxEntries int // 0 = unlimited

	done chan struct{} // closed by Close() to stop the background sync goroutine
}

// New creates a Vault configured for the given config directory.
// Call Load() then ImportZshHistory() to populate.
func New(configDir, ignorePfx string, maxEntries int) *Vault {
	return &Vault{
		done:       make(chan struct{}),
		vaultPath:  filepath.Join(configDir, "vault.enc"),
		keyPath:    filepath.Join(configDir, "vault.key"),
		ignorePfx:  ignorePfx,
		maxEntries: maxEntries,
		index:      make(map[string]struct{}),
	}
}

// Close flushes dirty data and stops the background sync goroutine. Safe to call multiple times.
func (v *Vault) Close() {
	select {
	case <-v.done:
		return
	default:
		close(v.done)
	}
	if err := v.Save(); err != nil {
		log.Printf("vault: flush on close failed: %v", err)
	}
}

// Load reads the encrypted vault file. Starts empty if the file is missing.
func (v *Vault) Load() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	data, err := os.ReadFile(v.vaultPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("vault: read: %w", err)
	}

	key, err := loadOrCreateKey(v.keyPath)
	if err != nil {
		return fmt.Errorf("vault: load key: %w", err)
	}

	plain, err := decrypt(data, key)
	if err != nil {
		return fmt.Errorf("vault: decrypt: %w", err)
	}

	for _, line := range strings.Split(string(plain), "\n") {
		cmd := strings.TrimSpace(line)
		if cmd == "" {
			continue
		}
		if _, exists := v.index[cmd]; !exists {
			v.commands = append(v.commands, cmd)
			v.index[cmd] = struct{}{}
		}
	}
	return nil
}

// ImportZshHistory parses a zsh history file and merges new entries.
// Commands matching the ignore prefix are skipped.
func (v *Vault) ImportZshHistory(histPath string) error {
	commands, err := ParseZshHistory(histPath)
	if err != nil {
		return fmt.Errorf("vault: import zsh history: %w", err)
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	for _, cmd := range commands {
		if v.ignorePfx != "" && strings.HasPrefix(cmd, v.ignorePfx) {
			continue
		}
		if _, exists := v.index[cmd]; !exists {
			v.commands = append(v.commands, cmd)
			v.index[cmd] = struct{}{}
			v.dirty = true
		}
	}
	return nil
}

// Save writes the vault to disk encrypted. No-op if nothing changed.
func (v *Vault) Save() error {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.dirty {
		return nil
	}

	data := []byte(strings.Join(v.commands, "\n"))

	key, err := loadOrCreateKey(v.keyPath)
	if err != nil {
		return fmt.Errorf("vault: load key: %w", err)
	}

	enc, err := encrypt(data, key)
	if err != nil {
		return fmt.Errorf("vault: encrypt: %w", err)
	}

	if err := os.WriteFile(v.vaultPath, enc, 0o600); err != nil {
		return fmt.Errorf("vault: write: %w", err)
	}
	v.dirty = false
	return nil
}

// Suggest returns the completion tail for a prefix-matched command.
// skip=0 returns the most recent match, skip=1 the next, etc.
// Returns empty string if no match at the given skip offset.
//
// Algorithm: for each history command (most recent first), check if the line
// ends with a prefix of the command. The longest such prefix wins. This handles
// arbitrary prompt formats without needing shell integration.
func (v *Vault) Suggest(line string, skip int) string {
	line = strings.TrimRight(line, " ")
	if len(line) < 2 {
		return ""
	}

	v.mu.RLock()
	defer v.mu.RUnlock()

	matched := 0
	for i := len(v.commands) - 1; i >= 0; i-- {
		cmd := v.commands[i]
		if len(cmd) < 2 {
			continue
		}

		// Check if line ends with a prefix of cmd.
		// Try the longest prefix first (most specific match).
		maxPfx := len(cmd)
		if maxPfx > len(line) {
			maxPfx = len(line)
		}
		for pfxLen := maxPfx; pfxLen >= 2; pfxLen-- {
			if strings.HasSuffix(line, cmd[:pfxLen]) && len(cmd) > pfxLen {
				if matched == skip {
					return cmd[pfxLen:]
				}
				matched++
				break
			}
		}
	}
	return ""
}

// Add inserts a command into the vault. Duplicates are moved to the end
// (most recent position). Commands matching the ignore prefix are skipped.
// When maxEntries > 0, the oldest entry is evicted to stay within the cap.
func (v *Vault) Add(cmd string) {
	if v.ignorePfx != "" && strings.HasPrefix(cmd, v.ignorePfx) {
		return
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	if _, exists := v.index[cmd]; exists {
		// Remove existing entry — search from end since duplicates are typically recent.
		for i := len(v.commands) - 1; i >= 0; i-- {
			if v.commands[i] == cmd {
				v.commands = append(v.commands[:i], v.commands[i+1:]...)
				break
			}
		}
	} else {
		// Evict oldest entry when cap is reached.
		if v.maxEntries > 0 && len(v.commands) >= v.maxEntries {
			oldest := v.commands[0]
			v.commands = v.commands[1:]
			delete(v.index, oldest)
		}
	}
	v.commands = append(v.commands, cmd)
	v.index[cmd] = struct{}{}
	v.dirty = true
}

// Len returns the number of stored commands.
func (v *Vault) Len() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.commands)
}

// Init loads the vault and imports zsh history in the background.
// syncInterval > 0 enables periodic re-import of zsh history on that cadence.
// Errors are logged, not returned — the vault degrades gracefully.
func Init(configDir, historyPath, ignorePfx string, maxEntries int, syncInterval time.Duration) *Vault {
	v := New(configDir, ignorePfx, maxEntries)

	go func() {
		if err := v.Load(); err != nil {
			log.Printf("vault load: %v", err)
		}

		if historyPath == "" {
			log.Printf("vault: loaded %d commands (no history path)", v.Len())
			return
		}

		syncHistory := func() {
			if err := v.ImportZshHistory(historyPath); err != nil {
				log.Printf("vault history import: %v", err)
				return
			}
			if err := v.Save(); err != nil {
				log.Printf("vault save: %v", err)
			}
		}

		syncHistory()
		log.Printf("vault: loaded %d commands", v.Len())

		if syncInterval <= 0 {
			return
		}
		ticker := time.NewTicker(syncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				syncHistory()
			case <-v.done:
				return
			}
		}
	}()

	return v
}
