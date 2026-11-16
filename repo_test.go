package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepositoryDatabaseCacheFreshness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	if err := os.WriteFile(path, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	if !repositoryDatabaseIsFresh(path, now) {
		t.Fatal("new repository database was not fresh")
	}

	old := now.Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if repositoryDatabaseIsFresh(path, now) {
		t.Fatal("one-hour-old repository database was fresh")
	}

	future := now.Add(time.Minute)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if repositoryDatabaseIsFresh(path, now) {
		t.Fatal("future-dated repository database was fresh")
	}
}
