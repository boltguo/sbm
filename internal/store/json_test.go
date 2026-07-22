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
