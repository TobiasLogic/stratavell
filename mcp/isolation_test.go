package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
	"github.com/TobiasLogic/stratavell/summarize"
)

func TestMCPCrossSessionSummaryIsolation(t *testing.T) {
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
	e, err := engine.Open(context.Background(), engine.Options{Path: filepath.Join(t.TempDir(), "m.db"), Policy: p, Summarizer: summarize.Extractive{}, SyncIndex: true, Manual: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	s, _ := e.OpenSession(ctx, "alpha", engine.SessionConfig{})
	a, _ := s.Agent(ctx, "main")
	for i := 0; i < 60; i++ {
		a.Append(ctx, msg.Text(msg.RoleUser, fmt.Sprintf("step %d", i)))
		a.Assemble(ctx, engine.AssembleRequest{System: "sys", IncludeMemoryTools: true})
		e.RunJobs(ctx)
		id := fmt.Sprintf("c%d", i)
		a.Append(ctx, msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartToolCall, ToolCallID: id, ToolName: "bash", Input: json.RawMessage(`{"command":"cat module"}`)}}})
		a.Append(ctx, msg.ToolResult(id, "bash", strings.Repeat(fmt.Sprintf("alpha module %d secret value_%d\n", i, i*7), 60), false, msg.Meta{}))
	}
	var ids []string
	rows, err := e.DB().R.Query("SELECT id FROM summaries WHERE session_id='alpha'")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		t.Fatal("no summaries in alpha")
	}
	var in strings.Builder
	in.WriteString(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}` + "\n")
	for i, id := range ids {
		for j, tool := range []string{"memory_get_summary_lineage", "memory_expand"} {
			fmt.Fprintf(&in, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{"id":%q}}}`+"\n", 1+2*i+j, tool, id)
		}
	}
	for _, passive := range []bool{false, true} {
		var out bytes.Buffer
		srv := &Server{E: e, Session: "beta", Agent: "main", Version: "test", Passive: passive}
		if err := srv.Serve(ctx, strings.NewReader(in.String()), &out); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if len(lines) != 1+2*len(ids) {
			t.Fatalf("expected %d responses, got %d", 1+2*len(ids), len(lines))
		}
		for _, l := range lines[1:] {
			if !strings.Contains(l, `"isError":true`) || strings.Contains(l, "seq_lo") || strings.Contains(l, "alpha module") {
				t.Fatalf("MCP (passive %v) leaked another session's summary: %s", passive, l)
			}
		}
	}
}
