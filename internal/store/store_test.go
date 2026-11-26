package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathsWithURICharacters(t *testing.T) {
	base := t.TempDir()
	names := []string{"a#b", "e%20f", "plain"}
	if runtime.GOOS != "windows" {
		names = append(names, "c?d")
	}
	for _, name := range names {
		dir := filepath.Join(base, name)
		db, err := Open(context.Background(), filepath.Join(dir, "x.db"), Options{})
		if err != nil {
			t.Fatal(err)
		}
		var mode string
		db.W.QueryRow("PRAGMA journal_mode").Scan(&mode)
		var fk int
		db.W.QueryRow("PRAGMA foreign_keys").Scan(&fk)
		db.Close()
		if mode != "wal" || fk != 1 {
			t.Fatalf("%s: journal_mode=%s foreign_keys=%d", name, mode, fk)
		}
		if _, err := os.Stat(filepath.Join(dir, "x.db")); err != nil {
			t.Fatalf("%s: database not created at the requested path: %v", name, err)
		}
	}
}

func TestBlobRoundTripAndCorruption(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "b.db"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	data := make([]byte, 100000)
	for i := range data {
		data[i] = byte(i % 7)
	}
	var h string
	err = db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		h, _, err = PutBlob(ctx, tx, data, DefaultBlobPolicy())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetBlob(ctx, db.R, h)
	if err != nil || len(got) != len(data) {
		t.Fatalf("round trip: %v", err)
	}
	var codec string
	var stored int
	db.R.QueryRow("SELECT codec, stored_size FROM blobs WHERE hash=?", h).Scan(&codec, &stored)
	if codec != CodecDeflate || stored >= len(data)/2 {
		t.Fatalf("expected compression, got %s %d", codec, stored)
	}
	db.W.Exec("UPDATE blobs SET data=x'00' WHERE hash=?", h)
	if _, err := GetBlob(ctx, db.R, h); err == nil {
		t.Fatal("corruption not detected")
	}
}
