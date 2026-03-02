package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// isRunningFromAppBundle detects if the app is running from a macOS .app bundle.
func isRunningFromAppBundle() bool {
	if runtime.GOOS != "darwin" {
		return false
	}

	// Get the executable path
	execPath, err := os.Executable()
	if err != nil {
		return false
	}

	// On macOS, app bundles have the structure:
	// /path/to/AppName.app/Contents/MacOS/executable
	// Check if the path contains ".app/Contents/MacOS"
	return strings.Contains(execPath, ".app/Contents/MacOS")
}

// getInitialDirectory returns the appropriate initial directory for new tabs/panes.
// When running from a .app bundle, always returns the user's home directory.
// Otherwise, returns the provided directory or current working directory.
func getInitialDirectory(requestedDir string) string {
	// If running from .app bundle, always use home directory
	if isRunningFromAppBundle() {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}

	// If a specific directory was requested and it exists, use it
	if requestedDir != "" {
		if info, err := os.Stat(requestedDir); err == nil && info.IsDir() {
			return requestedDir
		}
	}

	// Default to current working directory
	cwd, err := os.Getwd()
	if err != nil {
		// Last resort: home directory
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return "/"
	}

	// If cwd is inside .app bundle, use home directory instead
	if strings.Contains(cwd, ".app/Contents/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}

	return cwd
}

// sanitizeDirectory ensures the directory is valid for starting a shell.
// Returns home directory if the provided path is inside a .app bundle.
func sanitizeDirectory(dir string) string {
	if dir == "" {
		return getInitialDirectory("")
	}

	// Check if directory is inside .app bundle
	absPath, err := filepath.Abs(dir)
	if err == nil && strings.Contains(absPath, ".app/Contents/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}

	// Verify directory exists
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return dir
	}

	// Fallback to home
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}

	return "/"
}