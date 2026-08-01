// Package atomic provides crash-safe local file and directory operations.
package atomic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically: it writes to a temp file in
// the same directory, fsyncs, then renames into place. If the rename
// fails, the temp file is removed.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	return writeFile(path, data, mode, false)
}

// WriteFileExec writes data to path atomically and marks the resulting
// file as executable. On Windows, the executable bit is recorded but
// not enforced by the OS.
func WriteFileExec(path string, data []byte, mode os.FileMode) error {
	return writeFile(path, data, mode, true)
}

func writeFile(path string, data []byte, mode os.FileMode, executable bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".omp-sync-tmp-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename fails.
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}

	if executable {
		// os.Rename preserves the inode but on Windows we lose the
		// exec bit. We set a "best-effort" mode of 0o755.
		_ = os.Chmod(path, 0o755)
	}
	return nil
}

// ReplaceDir atomically swaps srcDir into dstDir. The destination is first
// moved to a backup location, then srcDir is renamed into place, and the
// backup is removed. If any step fails, the backup is restored.
//
// If dstDir does not exist, srcDir is simply renamed into place.
func ReplaceDir(srcDir, dstDir string) error {
	if _, err := os.Stat(srcDir); err != nil {
		return fmt.Errorf("source: %w", err)
	}

	if _, err := os.Stat(dstDir); os.IsNotExist(err) {
		// Simple case: dst doesn't exist.
		if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
			return fmt.Errorf("mkdir parent: %w", err)
		}
		if err := os.Rename(srcDir, dstDir); err != nil {
			return fmt.Errorf("rename: %w", err)
		}
		return nil
	}

	// Backup the destination.
	backup, err := randomBackupPath(dstDir)
	if err != nil {
		return err
	}
	if err := os.Rename(dstDir, backup); err != nil {
		return fmt.Errorf("backup dst: %w", err)
	}
	if err := os.Rename(srcDir, dstDir); err != nil {
		// Restore backup.
		_ = os.Rename(backup, dstDir)
		return fmt.Errorf("swap: %w", err)
	}
	if err := os.RemoveAll(backup); err != nil {
		// Non-fatal: there is an orphan backup directory.
		return nil
	}
	return nil
}

func randomBackupPath(dst string) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("rand: %w", err)
	}
	return dst + ".omp-sync-backup-" + hex.EncodeToString(buf[:]), nil
}

// CopyFile copies src to dst using streaming I/O. It does not fsync.
func CopyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src) //nolint:gosec // path is user-provided
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
