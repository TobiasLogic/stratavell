package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/TobiasLogic/stratavell/msg"
)

func TestPrunedTermsDropsCommonWordsWhenRareOnesExist(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "p.db"), Manual: true, SyncIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s, _ := e.OpenSession(ctx, "s", SessionConfig{})
	a, _ := s.Agent(ctx, "main")
	for i := 0; i < 260; i++ {
		text := fmt.Sprintf("go build ok; compute value %d for module m%d", i, i)
		if i%40 == 0 {
			text += " zeppelin"
		}
		if _, err := a.Append(ctx, msg.Text(msg.RoleUser, text)); err != nil {
			t.Fatal(err)
		}
	}
	keep := a.prunedTerms(ctx, "zeppelin compute value")
	for _, common := range []string{"compute", "value"} {
		if slices.Contains(keep, common) {
			t.Fatalf("common term %q kept next to a rare one: %v", common, keep)
		}
	}
	if !slices.Contains(keep, "zeppelin") {
		t.Fatalf("rare term dropped: %v", keep)
	}
	if keep := a.prunedTerms(ctx, "go zeppelin"); slices.Contains(keep, "go") {
		t.Fatalf("two-letter common term treated as rare: %v", keep)
	}
	if keep := a.prunedTerms(ctx, "compute value"); len(keep) == 0 || len(keep) > 2 {
		t.Fatalf("all-common query should keep the one or two rarest terms: %v", keep)
	}
}

func TestPathOnlyQueryUsesPathIndex(t *testing.T) {
	ctx := context.Background()
	e, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "q.db"), Manual: true, SyncIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s, _ := e.OpenSession(ctx, "s", SessionConfig{})
	a, _ := s.Agent(ctx, "main")
	a.Append(ctx, msg.Text(msg.RoleUser, "look at the config"))
	a.Append(ctx, msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartToolCall, ToolCallID: "r", ToolName: "read_file", Input: []byte(`{"path":"pkg/api/config.go"}`)}}})
	ref, err := a.Append(ctx, msg.ToolResult("r", "read_file", "package api\nconst RetryLimit = 7\n", false, msg.Meta{ContentType: msg.CTFileRead, Paths: []string{"pkg/api/config.go"}}))
	if err != nil {
		t.Fatal(err)
	}
	hits, err := a.Search(ctx, "pkg/api/config.go", SearchFilters{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != ref.ID {
		t.Fatalf("path query did not return the file read first: %+v", hits)
	}
}
