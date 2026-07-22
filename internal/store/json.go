package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type JSONFile[T any] struct {
	path string
	mu   sync.Mutex
}

func NewJSONFile[T any](path string) *JSONFile[T] { return &JSONFile[T]{path: path} }

func (f *JSONFile[T]) Path() string { return f.path }

func (f *JSONFile[T]) Load() (T, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value, err := readJSON[T](f.path)
	if err == nil {
		return value, nil
	}
	backup, backupErr := readJSON[T](f.path + ".bak")
	if backupErr == nil {
		return backup, nil
	}
	var zero T
	return zero, fmt.Errorf("load %s: primary: %v; backup: %v", filepath.Base(f.path), err, backupErr)
}

func (f *JSONFile[T]) Save(value T) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return writeJSONAtomic(f.path, value, true)
}

func (f *JSONFile[T]) SaveWithoutBackup(value T) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return writeJSONAtomic(f.path, value, false)
}

func readJSON[T any](path string) (T, error) {
	var value T
	data, err := os.ReadFile(path)
	if err != nil {
		return value, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return value, err
	}
	if err := ensureEOF(dec); err != nil {
		return value, err
	}
	return value, nil
}

func ensureEOF(dec *json.Decoder) error {
	var extra any
	err := dec.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func writeJSONAtomic(path string, value any, backup bool) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if backup {
		if _, err := os.Stat(path); err == nil {
			if err := copyAtomic(path, path+".bak"); err != nil {
				return fmt.Errorf("backup: %w", err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func copyAtomic(source, target string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".backup.*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}
