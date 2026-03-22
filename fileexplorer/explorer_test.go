package fileexplorer

import (
	"os"
	"path/filepath"
	"testing"
)

// setupTestDir creates a temp directory with a known structure:
//
//	root/
//	  alpha/
//	    nested.txt
//	  beta/
//	  file_a.txt
//	  file_b.txt
//	  .hidden
func setupTestDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "alpha"), 0o750)
	os.MkdirAll(filepath.Join(root, "beta"), 0o750)
	os.WriteFile(filepath.Join(root, "alpha", "nested.txt"), []byte("n"), 0o600)
	os.WriteFile(filepath.Join(root, "file_a.txt"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(root, "file_b.txt"), []byte("b"), 0o600)
	os.WriteFile(filepath.Join(root, ".hidden"), []byte("h"), 0o600)
	return root
}

// --- LoadChildren ---

func TestLoadChildren_DirsFirst(t *testing.T) {
	root := setupTestDir(t)
	entries, err := LoadChildren(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Expect: alpha, beta (dirs), then file_a.txt, file_b.txt (files).
	// .hidden should be skipped.
	if len(entries) != 4 {
		t.Fatalf("got %d entries, want 4", len(entries))
	}
	if !entries[0].IsDir || entries[0].Name != "alpha" {
		t.Errorf("entry[0] = %q dir=%v, want alpha dir", entries[0].Name, entries[0].IsDir)
	}
	if !entries[1].IsDir || entries[1].Name != "beta" {
		t.Errorf("entry[1] = %q dir=%v, want beta dir", entries[1].Name, entries[1].IsDir)
	}
	if entries[2].IsDir || entries[2].Name != "file_a.txt" {
		t.Errorf("entry[2] = %q, want file_a.txt", entries[2].Name)
	}
	if entries[3].IsDir || entries[3].Name != "file_b.txt" {
		t.Errorf("entry[3] = %q, want file_b.txt", entries[3].Name)
	}
}

func TestLoadChildren_HiddenFilesFiltered(t *testing.T) {
	root := setupTestDir(t)
	entries, err := LoadChildren(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == ".hidden" {
			t.Error("hidden file should be filtered")
		}
	}
}

func TestLoadChildren_EmptyDir(t *testing.T) {
	root := t.TempDir()
	entries, err := LoadChildren(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestLoadChildren_Depth(t *testing.T) {
	root := setupTestDir(t)
	entries, err := LoadChildren(root, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Depth != 3 {
			t.Errorf("entry %q depth = %d, want 3", e.Name, e.Depth)
		}
	}
}

// --- BuildTree ---

func TestBuildTree_HasSpecialEntries(t *testing.T) {
	root := setupTestDir(t)
	entries, err := BuildTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 2 {
		t.Fatal("expected at least . and .. entries")
	}
	if entries[0].Name != "." {
		t.Errorf("first entry = %q, want '.'", entries[0].Name)
	}
	if entries[1].Name != ".." {
		t.Errorf("second entry = %q, want '..'", entries[1].Name)
	}
}

func TestBuildTree_TotalCount(t *testing.T) {
	root := setupTestDir(t)
	entries, err := BuildTree(root)
	if err != nil {
		t.Fatal(err)
	}
	// . + .. + alpha + beta + file_a.txt + file_b.txt = 6
	if len(entries) != 6 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name
		}
		t.Errorf("got %d entries %v, want 6", len(entries), names)
	}
}

// --- ExpandAt / CollapseAt ---

func TestExpandAt(t *testing.T) {
	root := setupTestDir(t)
	entries, _ := BuildTree(root)

	// Find "alpha" (should be at index 2 after . and ..).
	alphaIdx := -1
	for i, e := range entries {
		if e.Name == "alpha" {
			alphaIdx = i
			break
		}
	}
	if alphaIdx < 0 {
		t.Fatal("alpha not found")
	}

	expanded, err := ExpandAt(entries, alphaIdx)
	if err != nil {
		t.Fatal(err)
	}
	// alpha has 1 child: nested.txt.
	if len(expanded) != len(entries)+1 {
		t.Errorf("after expand: %d entries, want %d", len(expanded), len(entries)+1)
	}
	// The child should be right after alpha.
	child := expanded[alphaIdx+1]
	if child.Name != "nested.txt" {
		t.Errorf("child = %q, want nested.txt", child.Name)
	}
	if child.Depth != 1 {
		t.Errorf("child depth = %d, want 1", child.Depth)
	}
}

func TestExpandAt_AlreadyExpanded(t *testing.T) {
	root := setupTestDir(t)
	entries, _ := BuildTree(root)
	alphaIdx := FindIdx(entries, filepath.Join(root, "alpha"))
	expanded, _ := ExpandAt(entries, alphaIdx)
	// Second expand should be a no-op.
	again, _ := ExpandAt(expanded, alphaIdx)
	if len(again) != len(expanded) {
		t.Errorf("double expand changed length: %d → %d", len(expanded), len(again))
	}
}

func TestExpandAt_File(t *testing.T) {
	root := setupTestDir(t)
	entries, _ := BuildTree(root)
	fileIdx := FindIdx(entries, filepath.Join(root, "file_a.txt"))
	result, _ := ExpandAt(entries, fileIdx)
	if len(result) != len(entries) {
		t.Error("expanding a file should be a no-op")
	}
}

func TestCollapseAt(t *testing.T) {
	root := setupTestDir(t)
	entries, _ := BuildTree(root)
	alphaIdx := FindIdx(entries, filepath.Join(root, "alpha"))
	expanded, _ := ExpandAt(entries, alphaIdx)
	collapsed := CollapseAt(expanded, alphaIdx)
	if len(collapsed) != len(entries) {
		t.Errorf("after collapse: %d entries, want %d", len(collapsed), len(entries))
	}
}

func TestCollapseAt_NotExpanded(t *testing.T) {
	root := setupTestDir(t)
	entries, _ := BuildTree(root)
	alphaIdx := FindIdx(entries, filepath.Join(root, "alpha"))
	result := CollapseAt(entries, alphaIdx)
	if len(result) != len(entries) {
		t.Error("collapsing non-expanded dir should be no-op")
	}
}

// --- CurrentDir ---

func TestCurrentDir_OnDir(t *testing.T) {
	entries := []Entry{
		{Path: "/tmp/foo", Name: "foo", IsDir: true},
	}
	if got := CurrentDir(entries, 0); got != "/tmp/foo" {
		t.Errorf("got %q, want /tmp/foo", got)
	}
}

func TestCurrentDir_OnFile(t *testing.T) {
	entries := []Entry{
		{Path: "/tmp/foo/bar.txt", Name: "bar.txt", IsDir: false},
	}
	if got := CurrentDir(entries, 0); got != "/tmp/foo" {
		t.Errorf("got %q, want /tmp/foo", got)
	}
}

func TestCurrentDir_OutOfBounds(t *testing.T) {
	if got := CurrentDir(nil, 0); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// --- FindIdx ---

func TestFindIdx_Found(t *testing.T) {
	entries := []Entry{
		{Path: "/a"},
		{Path: "/b"},
		{Path: "/c"},
	}
	if got := FindIdx(entries, "/b"); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestFindIdx_NotFound(t *testing.T) {
	entries := []Entry{{Path: "/a"}}
	if got := FindIdx(entries, "/z"); got != -1 {
		t.Errorf("got %d, want -1", got)
	}
}

// --- CreateFile / CreateDir ---

func TestCreateFile(t *testing.T) {
	dir := t.TempDir()
	path, err := CreateFile(dir, "test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("file not created")
	}
}

func TestCreateDir(t *testing.T) {
	dir := t.TempDir()
	path, err := CreateDir(dir, "subdir")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

// --- RenamePath ---

func TestRenamePath(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	os.WriteFile(old, []byte("x"), 0o600)

	newPath, err := RenamePath(old, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(newPath) != "new.txt" {
		t.Errorf("new name = %q, want new.txt", filepath.Base(newPath))
	}
	if _, err := os.Stat(newPath); os.IsNotExist(err) {
		t.Error("renamed file not found")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old file still exists")
	}
}

// --- DeletePath ---

func TestDeletePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "victim.txt")
	os.WriteFile(path, []byte("x"), 0o600)

	if err := DeletePath(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file still exists after delete")
	}
}

// --- SearchCurrentLevel ---

func TestSearchCurrentLevel(t *testing.T) {
	root := setupTestDir(t)
	results := SearchCurrentLevel(root, "file")
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
}

func TestSearchCurrentLevel_CaseInsensitive(t *testing.T) {
	root := setupTestDir(t)
	results := SearchCurrentLevel(root, "ALPHA")
	if len(results) != 1 {
		t.Errorf("got %d results, want 1", len(results))
	}
}

func TestSearchCurrentLevel_Empty(t *testing.T) {
	root := setupTestDir(t)
	results := SearchCurrentLevel(root, "")
	if results != nil {
		t.Error("empty query should return nil")
	}
}

func TestSearchCurrentLevel_NoMatch(t *testing.T) {
	root := setupTestDir(t)
	results := SearchCurrentLevel(root, "zzzzz")
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

// --- Safety guard tests ---

func TestDeletePath_RejectsEmpty(t *testing.T) {
	if err := DeletePath(""); err == nil {
		t.Error("DeletePath('') should return error")
	}
}

func TestDeletePath_RejectsRoot(t *testing.T) {
	if err := DeletePath("/"); err == nil {
		t.Error("DeletePath('/') should return error")
	}
}

func TestDeletePath_RejectsDot(t *testing.T) {
	if err := DeletePath("."); err == nil {
		t.Error("DeletePath('.') should return error")
	}
}

func TestDeletePath_ValidPath(t *testing.T) {
	root := setupTestDir(t)
	path := filepath.Join(root, "file_a.txt")
	if err := DeletePath(path); err != nil {
		t.Fatalf("DeletePath(%q) = %v", path, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be deleted")
	}
}

func TestRenamePath_RejectsPathSeparator(t *testing.T) {
	root := setupTestDir(t)
	old := filepath.Join(root, "file_a.txt")
	_, err := RenamePath(old, "../escape.txt")
	if err == nil {
		t.Error("RenamePath with ../ should return error")
	}
}

func TestRenamePath_RejectsSlash(t *testing.T) {
	root := setupTestDir(t)
	old := filepath.Join(root, "file_a.txt")
	_, err := RenamePath(old, "sub/name.txt")
	if err == nil {
		t.Error("RenamePath with / should return error")
	}
}

func TestRenamePath_RejectsDotDot(t *testing.T) {
	root := setupTestDir(t)
	old := filepath.Join(root, "file_a.txt")
	_, err := RenamePath(old, "..")
	if err == nil {
		t.Error("RenamePath with '..' should return error")
	}
}

func TestRenamePath_Valid(t *testing.T) {
	root := setupTestDir(t)
	old := filepath.Join(root, "file_a.txt")
	newPath, err := RenamePath(old, "renamed.txt")
	if err != nil {
		t.Fatalf("RenamePath = %v", err)
	}
	expected := filepath.Join(root, "renamed.txt")
	if newPath != expected {
		t.Errorf("newPath = %q, want %q", newPath, expected)
	}
	if _, err := os.Stat(expected); err != nil {
		t.Error("renamed file should exist")
	}
}

func TestCopyPath_RejectsDuplicateDst(t *testing.T) {
	root := setupTestDir(t)
	src := filepath.Join(root, "file_a.txt")
	// Copy once.
	if err := CopyPath(src, filepath.Join(root, "alpha")); err != nil {
		t.Fatalf("first copy: %v", err)
	}
	// Copy again — should fail because dst already exists.
	err := CopyPath(src, filepath.Join(root, "alpha"))
	if err == nil {
		t.Error("CopyPath should error when destination exists")
	}
}

func TestMovePath_RejectsDuplicateDst(t *testing.T) {
	root := setupTestDir(t)
	// Create a second file to move.
	src := filepath.Join(root, "file_b.txt")
	// Copy file_a first to create a conflict.
	os.WriteFile(filepath.Join(root, "alpha", "file_b.txt"), []byte("conflict"), 0o600)

	err := MovePath(src, filepath.Join(root, "alpha"))
	if err == nil {
		t.Error("MovePath should error when destination exists")
	}
}
