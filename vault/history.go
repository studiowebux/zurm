package vault

import (
	"bufio"
	"os"
	"strings"
)

// ParseZshHistory reads a zsh history file and returns deduplicated commands.
// Handles both plain format (one command per line) and extended format
// (": timestamp:0;command"). Multi-line commands joined by trailing backslash
// are concatenated.
func ParseZshHistory(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	seen := make(map[string]struct{})
	var commands []string
	var continuation strings.Builder

	scanner := bufio.NewScanner(f)
	// Increase scanner buffer for long lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Handle backslash continuation.
		if continuation.Len() > 0 {
			continuation.WriteString("\n")
			if strings.HasSuffix(line, "\\") {
				continuation.WriteString(strings.TrimSuffix(line, "\\"))
				continue
			}
			continuation.WriteString(line)
			line = continuation.String()
			continuation.Reset()
		} else if strings.HasSuffix(line, "\\") {
			continuation.WriteString(strings.TrimSuffix(line, "\\"))
			continue
		}

		cmd := parseHistoryLine(line)
		if cmd == "" {
			continue
		}
		if _, exists := seen[cmd]; exists {
			continue
		}
		seen[cmd] = struct{}{}
		commands = append(commands, cmd)
	}

	// Flush any incomplete continuation.
	if continuation.Len() > 0 {
		cmd := parseHistoryLine(continuation.String())
		if cmd != "" {
			if _, exists := seen[cmd]; !exists {
				commands = append(commands, cmd)
			}
		}
	}

	return commands, scanner.Err()
}

// parseHistoryLine extracts the command from a single history line.
// Extended format: ": 1234567890:0;command text"
// Plain format:    "command text"
func parseHistoryLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	// Extended format detection: starts with ": " followed by digits.
	if strings.HasPrefix(line, ": ") && len(line) > 4 {
		// Find the semicolon that separates metadata from command.
		if idx := strings.Index(line, ";"); idx >= 0 {
			return strings.TrimSpace(line[idx+1:])
		}
	}

	return line
}
