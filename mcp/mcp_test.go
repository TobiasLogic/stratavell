package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/summarize"
)

func TestStdioSession(t *testing.T) {
	e, err := engine.Open(context.Background(), engine.Options{Path: filepath.Join(t.TempDir(), "m.db"), Summarizer: summarize.Extractive{}, SyncIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_archive","arguments":{"title":"decision","content":"We chose SQLite WAL for the journal because it needs no server. The port is 7077."}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"memory_search","arguments":{"query":"which port journal"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"bogus"}`,
	}, "\n")
	var out bytes.Buffer
	s := &Server{E: e, Session: "s", Agent: "mcp", Version: "test", Passive: true}
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 responses, got %d:\n%s", len(lines), out.String())
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(lines[0]), &init)
	if init.Result.ProtocolVersion != "2025-06-18" {
		t.Fatalf("version negotiation: %s", lines[0])
	}
	if !strings.Contains(lines[1], "memory_archive") || !strings.Contains(lines[1], "memory_expand") {
		t.Fatalf("tools/list: %s", lines[1])
	}
	if !strings.Contains(lines[2], "archived as ev_") {
		t.Fatalf("archive: %s", lines[2])
	}
	if !strings.Contains(lines[3], "7077") {
		t.Fatalf("search did not find archived content: %s", lines[3])
	}
	if !strings.Contains(lines[4], `"isError":true`) {
		t.Fatalf("unknown tool not an error: %s", lines[4])
	}
	if !strings.Contains(lines[5], "-32601") {
		t.Fatalf("unknown method: %s", lines[5])
	}
}
