package zserver

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	serverStartupTimeout = 2 * time.Second       // max wait for server to become ready
	serverPollInterval   = 50 * time.Millisecond  // poll frequency while waiting for server
	socketProbeTimeout   = 200 * time.Millisecond // dial timeout for reachability check
)

// EnsureServer checks if zurm-server is running at the given socket path.
// If not, it spawns the server binary as a detached background process.
// Returns the resolved socket address to connect to, or an error.
//
// socketPath may be empty, in which case the default path
// (~/.config/zurm/server.sock) is used.
//
// serverBinary may be empty, in which case the binary is located by:
//  1. Same directory as the running zurm executable (os.Executable)
//  2. PATH lookup for "zurm-server"
func EnsureServer(socketPath, serverBinary string) (string, error) {
	addr := ResolveSocket(socketPath)

	// Fast path: server already running.
	if isReachable(addr) {
		return addr, nil
	}

	bin, err := findBinary(serverBinary)
	if err != nil {
		return "", fmt.Errorf("zurm-server binary not found: %w", err)
	}

	if err := spawnServer(bin, addr); err != nil {
		return "", fmt.Errorf("spawn zurm-server: %w", err)
	}

	// Wait for the socket to appear and accept connections.
	deadline := time.Now().Add(serverStartupTimeout)
	for time.Now().Before(deadline) {
		if isReachable(addr) {
			log.Printf("zserver/autostart: server ready at %s", addr)
			return addr, nil
		}
		time.Sleep(serverPollInterval)
	}

	return "", fmt.Errorf("zurm-server did not become ready at %s within %s", addr, serverStartupTimeout)
}

// ResolveSocket returns the canonical socket path, applying the default when
// socketPath is empty.
func ResolveSocket(socketPath string) string {
	if socketPath != "" {
		return socketPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/zurm-server.sock"
	}
	return filepath.Join(home, ".config", "zurm", "server.sock")
}

// isReachable dials the Unix socket and returns true when the server answers.
func isReachable(addr string) bool {
	conn, err := net.DialTimeout("unix", addr, socketProbeTimeout)
	if err != nil {
		return false
	}
	conn.Close() // #nosec G104 — probe connection; only checking reachability, error is irrelevant
	return true
}

// findBinary locates the zurm-server executable.
// Priority: explicit cfg.Server.Binary → sibling of os.Executable() → PATH.
func findBinary(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err == nil {
			return configured, nil
		}
		return "", fmt.Errorf("configured binary %q not found", configured)
	}

	// Look next to the running zurm binary.
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), "zurm-server")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	// Fall back to PATH.
	if path, err := exec.LookPath("zurm-server"); err == nil {
		return path, nil
	}

	return "", fmt.Errorf("not found in sibling directory or PATH")
}

// spawnServer starts zurm-server as a detached background process.
// Stdout/stderr are redirected to ~/.config/zurm/server.log.
func spawnServer(binary, socketPath string) error {
	logPath, err := serverLogPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return fmt.Errorf("create log dir: %w", err)
	}

	// Open log file in append mode so multiple starts accumulate.
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 — path from UserHomeDir
	if err != nil {
		return fmt.Errorf("open server log: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(binary, "--socket", socketPath) // #nosec G204 — binary resolved from trusted locations
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Detach: new session so the process is not a child of zurm.
	// When zurm exits, the server keeps running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("zserver/autostart: spawn server: %w", err)
	}

	log.Printf("zserver/autostart: spawned zurm-server (pid %d) at %s — log: %s",
		cmd.Process.Pid, socketPath, logPath)

	// Disown the process — we do not Wait() on it.
	go func() { _ = cmd.Wait() }()

	return nil
}

// serverLogPath returns the path to the zurm-server log file.
func serverLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "zurm", "server.log"), nil
}
