package db

import (
	"context"
	"testing"
)

func TestOpenInMemory(t *testing.T) {
	conn, err := Open(Config{DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	defer conn.Close()

	var fkEnabled int
	err = conn.QueryRowContext(context.Background(), "PRAGMA foreign_keys;").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("failed to query PRAGMA foreign_keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("expected foreign_keys = 1, got %d", fkEnabled)
	}
}
