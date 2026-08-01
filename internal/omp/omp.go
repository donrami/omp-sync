// Package omp discovers the omp config directory and detects running
// omp processes.
//
// The config directory is discovered in this order:
//
//  1. OMP_SYNC_OMP_DIR environment variable (override).
//  2. $XDG_CONFIG_HOME/omp (Linux/macOS) or %AppData%\omp (Windows).
//  3. $XDG_CONFIG_HOME/oh-my-pi.
//  4. ~/.omp/.
//
// All candidates are checked for existence; the first that exists is used.
package omp

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrNotFound is returned when no omp config directory can be located.
var ErrNotFound = errors.New("omp config directory not found")

// Discover returns the absolute path to the omp config directory.
// Returns ErrNotFound if no candidate exists.
func Discover() (string, error) {
	candidates := candidates()
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			abs, absErr := filepath.Abs(c)
			if absErr != nil {
				return c, nil
			}
			return abs, nil
		}
	}
	return "", ErrNotFound
}

// ValidateLayout returns nil if dir looks like a plausible omp config dir.
// A directory is considered plausible if it contains any of: commands/,
// agents/, snippets/, themes/, plugins/, omp.toml, omp.json.
func ValidateLayout(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}
	hints := map[string]bool{
		"commands": true, "agents": true, "snippets": true,
		"themes": true, "plugins": true,
	}
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		if hints[name] {
			return nil
		}
		if name == "omp.toml" || name == "omp.json" || name == "config.toml" {
			return nil
		}
	}
	return errors.New("directory does not look like an omp config tree")
}

// IsRunning returns true if an omp process is detected on the current
// machine. Best-effort: false negatives are acceptable.
func IsRunning() bool {
	switch runtime.GOOS {
	case "linux":
		return linuxRunning()
	case "darwin":
		return macRunning()
	case "windows":
		return windowsRunning()
	default:
		return false
	}
}

func linuxRunning() bool {
	// Walk /proc and grep comm for "omp".
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid := e.Name()
		if !isAllDigits(pid) {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", pid, "comm"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == "omp" {
			return true
		}
	}
	return false
}

func macRunning() bool {
	out, err := exec.Command("pgrep", "-x", "omp").Output()
	//nolint:gosec // pgrep is a fixed binary
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

func windowsRunning() bool {
	out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq omp.exe").Output()
	//nolint:gosec // tasklist is a fixed binary
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), "omp.exe")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func candidates() []string {
	var out []string
	if v := os.Getenv("OMP_SYNC_OMP_DIR"); v != "" {
		out = append(out, v)
	}

	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		if home != "" {
			xdg = filepath.Join(home, ".config")
		}
	}
	if xdg != "" {
		out = append(out, filepath.Join(xdg, "omp"))
		out = append(out, filepath.Join(xdg, "oh-my-pi"))
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		out = append(out, filepath.Join(home, ".omp"))
	}
	return out
}
