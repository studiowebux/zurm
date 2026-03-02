package fileexplorer

import (
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Entry represents a single node in the file explorer tree.
type Entry struct {
	Path     string
	Name     string
	IsDir    bool
	Depth    int
	Expanded bool
}

// Clipboard holds a pending copy or cut operation.
type Clipboard struct {
	Op   string // "copy" or "cut"
	Path string
}

// LoadChildren reads the directory at root and returns sorted entries at the given depth.
// Directories appear before files; each group is sorted alphabetically.
func LoadChildren(root string, depth int) ([]Entry, error) {
	f, err := os.Open(root) // #nosec G304 — file explorer opens user-selected filesystem paths by design
	if err != nil {
		return nil, err
	}
	defer f.Close()

	infos, err := f.Readdir(-1)
	if err != nil {
		return nil, err
	}

	var dirs, files []Entry
	for _, info := range infos {
		if info.Name()[0] == '.' {
			continue // skip hidden entries
		}
		e := Entry{
			Path:  filepath.Join(root, info.Name()),
			Name:  info.Name(),
			IsDir: info.IsDir(),
			Depth: depth,
		}
		if info.IsDir() {
			dirs = append(dirs, e)
		} else {
			files = append(files, e)
		}
	}

	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name < dirs[j].Name })
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })

	return append(dirs, files...), nil
}

// BuildTree returns the top-level entries for root at depth 0.
func BuildTree(root string) ([]Entry, error) {
	return LoadChildren(root, 0)
}

// ExpandAt inserts the children of entries[idx] immediately after idx.
// Returns the updated slice. The entry at idx is marked Expanded = true.
func ExpandAt(entries []Entry, idx int) ([]Entry, error) {
	if idx < 0 || idx >= len(entries) {
		return entries, nil
	}
	e := &entries[idx]
	if !e.IsDir || e.Expanded {
		return entries, nil
	}

	children, err := LoadChildren(e.Path, e.Depth+1)
	if err != nil {
		return entries, err
	}

	e.Expanded = true
	// Insert children after idx.
	tail := make([]Entry, len(entries[idx+1:]))
	copy(tail, entries[idx+1:])
	result := append(entries[:idx+1], children...)
	result = append(result, tail...)
	return result, nil
}

// CollapseAt removes all descendants of entries[idx] and marks it not expanded.
func CollapseAt(entries []Entry, idx int) []Entry {
	if idx < 0 || idx >= len(entries) {
		return entries
	}
	if !entries[idx].IsDir || !entries[idx].Expanded {
		return entries
	}
	entries[idx].Expanded = false
	baseDepth := entries[idx].Depth
	end := idx + 1
	for end < len(entries) && entries[end].Depth > baseDepth {
		end++
	}
	return append(entries[:idx+1], entries[end:]...)
}

// CurrentDir returns the directory path for the entry at cursor.
// If the entry is a directory, it returns entry.Path; otherwise filepath.Dir(entry.Path).
func CurrentDir(entries []Entry, cursor int) string {
	if cursor < 0 || cursor >= len(entries) {
		return ""
	}
	e := entries[cursor]
	if e.IsDir {
		return e.Path
	}
	return filepath.Dir(e.Path)
}

// DeletePath removes the file or directory at path (recursive).
func DeletePath(path string) error {
	return os.RemoveAll(path)
}

// RenamePath renames oldPath to a new name within the same directory.
// Returns the new full path.
func RenamePath(oldPath, newName string) (string, error) {
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		return "", err
	}
	return newPath, nil
}

// CreateFile creates an empty file named name inside dir.
// Returns the new file's full path.
func CreateFile(dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	f, err := os.Create(path) // #nosec G304 — file explorer creates files at user-selected paths by design
	if err != nil {
		return "", err
	}
	f.Close() // #nosec G104 — newly created empty file; close error is not actionable
	return path, nil
}

// CreateDir creates a directory named name inside dir (including any missing parents).
// Returns the new directory's full path.
func CreateDir(dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o750); err != nil {
		return "", err
	}
	return path, nil
}

// CopyPath copies src to dstDir, preserving directory structure when src is a directory.
func CopyPath(src, dstDir string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	dst := filepath.Join(dstDir, filepath.Base(src))
	if info.IsDir() {
		return copyDir(src, dst)
	}
	return copyFile(src, dst)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 — file explorer copies user-selected paths by design
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst) // #nosec G304
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// MovePath moves src into dstDir. Tries os.Rename first; falls back to copy + remove.
func MovePath(src, dstDir string) error {
	dst := filepath.Join(dstDir, filepath.Base(src))
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := CopyPath(src, dstDir); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// FindIdx returns the index of the entry matching path, or -1 if not found.
func FindIdx(entries []Entry, path string) int {
	for i, e := range entries {
		if e.Path == path {
			return i
		}
	}
	return -1
}
