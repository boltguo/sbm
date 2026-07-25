package store

import (
	"os"
	"path/filepath"
	"testing"
)

type testDocument struct {
	Version int    `json:"version"`
	Value   string `json:"value"`
}

func TestAtomicWritePermissionsAndBackupRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	file := NewJSONFile[testDocument](path)
	if err := file.Save(testDocument{Version: 1, Value: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(testDocument{Version: 1, Value: "second"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	recovered, err := file.Load()
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Value != "first" {
		t.Fatalf("backup value = %q, want first", recovered.Value)
	}
}

// The backup exists for the case where the primary is unreadable, so a save
// while the primary is corrupt must leave the last good copy intact.
func TestCorruptPrimaryIsNeverPromotedToBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	file := NewJSONFile[testDocument](path)
	if err := file.Save(testDocument{Version: 1, Value: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(testDocument{Version: 1, Value: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := file.Save(testDocument{Version: 1, Value: "third"}); err != nil {
		t.Fatal(err)
	}
	backup, err := readJSON[testDocument](path + ".bak")
	if err != nil {
		t.Fatalf("backup no longer parses: %v", err)
	}
	if backup.Value != "first" {
		t.Fatalf("backup value = %q, want the last good copy first", backup.Value)
	}
	loaded, err := file.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Value != "third" {
		t.Fatalf("primary value = %q, want third", loaded.Value)
	}
}
