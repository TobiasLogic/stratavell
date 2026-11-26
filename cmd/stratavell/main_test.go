package main

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
)

func TestEnvPrefersNewNameAndAcceptsLegacy(t *testing.T) {
	t.Setenv("STRATAVELL_DB", "")
	t.Setenv("PALIMPSEST_DB", "legacy.db")
	if got := env("DB"); got != "legacy.db" {
		t.Fatalf("legacy fallback: %q", got)
	}
	t.Setenv("STRATAVELL_DB", "new.db")
	if got := env("DB"); got != "new.db" {
		t.Fatalf("new name should win: %q", got)
	}
}

func TestResolveDBUsesLegacyFileOnlyWhenNewIsAbsent(t *testing.T) {
	t.Setenv("STRATAVELL_DB", "")
	t.Setenv("PALIMPSEST_DB", "")
	t.Chdir(t.TempDir())
	var c common
	if got := c.resolveDB(); got != defaultDB {
		t.Fatalf("empty dir: %q", got)
	}
	os.WriteFile(legacyDB, nil, 0o600)
	if got := c.resolveDB(); got != legacyDB {
		t.Fatalf("legacy only: %q", got)
	}
	os.WriteFile(defaultDB, nil, 0o600)
	if got := c.resolveDB(); got != defaultDB {
		t.Fatalf("both present: %q", got)
	}
}

func TestMigrateCopiesVerifiesAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	from := filepath.Join(dir, "palimpsest.db")
	to := filepath.Join(dir, "new", "stratavell.db")
	e, err := engine.Open(ctx, engine.Options{Path: from})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := e.OpenSession(ctx, "s", engine.SessionConfig{})
	a, _ := s.Agent(ctx, "main")
	if _, err := a.Append(ctx, msg.Text(msg.RoleUser, "keep this exact text")); err != nil {
		t.Fatal(err)
	}
	e.Close()
	sum := func(p string) [32]byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	before := sum(from)
	if err := cmdMigrate(ctx, []string{"--from", from, "--to", to}); err != nil {
		t.Fatal(err)
	}
	if sum(from) != before {
		t.Fatal("source database changed")
	}
	copyHash := sum(to)
	if err := cmdMigrate(ctx, []string{"--from", from, "--to", to}); err == nil {
		t.Fatal("second migrate should refuse to overwrite")
	}
	if sum(to) != copyHash {
		t.Fatal("existing target was modified")
	}
	e2, err := engine.Open(ctx, engine.Options{Path: to})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	s2, err := e2.LookupSession(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := s2.LookupAgent(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	hits, err := a2.Search(ctx, "exact text", engine.SearchFilters{}, 5)
	if err != nil || len(hits) == 0 {
		t.Fatalf("copied event not searchable: %v %v", err, hits)
	}
}
