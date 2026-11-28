package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
	"github.com/TobiasLogic/stratavell/server"
	"github.com/TobiasLogic/stratavell/summarize"
)

func compactingEngine(t *testing.T) *engine.Engine {
	t.Helper()
	p := engine.DefaultPolicy()
	p.Budget.ContextWindow = 16000
	p.Budget.MaxOutputTokens = 1600
	p.Budget.SafetyTokens = 200
	p.Budget.MaxObservationToken = 1300
	p.Compaction.LeafChunkTokens = 2000
	p.Compaction.MinChunkTokens = 400
	p.Compaction.SummaryMaxTokens = 400
	p.Compaction.RetryBackoff = engine.Duration(time.Millisecond)
	p.Compaction.JobTimeout = engine.Duration(2 * time.Second)
	e, err := engine.Open(context.Background(), engine.Options{Path: filepath.Join(t.TempDir(), "s.db"), Policy: p, Summarizer: summarize.Extractive{}, SyncIndex: true, Manual: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func fillSession(t *testing.T, e *engine.Engine, session string) []string {
	t.Helper()
	ctx := context.Background()
	s, err := e.OpenSession(ctx, session, engine.SessionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Agent(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		if _, err := a.Append(ctx, msg.Text(msg.RoleUser, fmt.Sprintf("step %d: inspect module %d of %s", i, i, session))); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Assemble(ctx, engine.AssembleRequest{System: "sys", IncludeMemoryTools: true}); err != nil {
			t.Fatal(err)
		}
		e.RunJobs(ctx)
		id := fmt.Sprintf("c%d", i)
		if _, err := a.Append(ctx, msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartToolCall, ToolCallID: id, ToolName: "bash", Input: json.RawMessage(`{"command":"cat module"}`)}}}); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for j := 0; j < 60; j++ {
			fmt.Fprintf(&b, "%s module %d line %d value_%d := compute(%d)\n", session, i, j, j, i*j)
		}
		if _, err := a.Append(ctx, msg.ToolResult(id, "bash", b.String(), false, msg.Meta{})); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := e.DB().R.Query("SELECT id FROM summaries WHERE session_id=?", session)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		t.Fatalf("session %s has no summaries", session)
	}
	return ids
}

func TestHTTPCrossSessionSummaryIsolation(t *testing.T) {
	e := compactingEngine(t)
	theirs := fillSession(t, e, "alpha")
	fillSession(t, e, "beta")
	ts := httptest.NewServer(server.New(e, ""))
	t.Cleanup(ts.Close)
	for _, id := range theirs {
		if code, out := call(t, ts, "GET", "/v1/sessions/beta/agents/main/lineage/"+id, "", nil); code != 404 {
			t.Fatalf("lineage of another session's summary %s: %d %v", id, code, out)
		}
		if code, out := call(t, ts, "GET", "/v1/sessions/alpha/agents/main/lineage/"+id, "", nil); code != 200 || out["node"] == nil {
			t.Fatalf("owner lineage of %s: %d %v", id, code, out)
		}
		for _, tool := range []string{"memory_get_summary_lineage", "memory_expand"} {
			code, out := call(t, ts, "POST", "/v1/sessions/beta/agents/main/tools/"+tool, "", map[string]any{"input": map[string]string{"id": id}})
			if b, _ := json.Marshal(out); code != 200 || out["is_error"] != true || strings.Contains(string(b), "seq_lo") || strings.Contains(string(b), "alpha module") {
				t.Fatalf("%s over HTTP leaked another session's summary %s: %d %s", tool, id, code, b)
			}
		}
	}
}
