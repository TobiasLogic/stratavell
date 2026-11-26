package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
	"github.com/TobiasLogic/palimpsest/summarize"
)

type script struct{ n int }

func (s *script) Generate(ctx context.Context, c *engine.Context) (msg.Message, msg.Usage, error) {
	s.n++
	switch s.n {
	case 1:
		return msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{
			{Type: msg.PartToolCall, ToolCallID: "a", ToolName: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
			{Type: msg.PartToolCall, ToolCallID: "b", ToolName: "memory_search", Input: json.RawMessage(`{"query":"vendor rule"}`)},
			{Type: msg.PartToolCall, ToolCallID: "c", ToolName: "broken", Input: json.RawMessage(`{}`)},
		}}, msg.Usage{InputTokens: 100}, nil
	default:
		return msg.Text(msg.RoleAssistant, "done"), msg.Usage{InputTokens: 120}, nil
	}
}

func TestRunHandlesToolsAndErrors(t *testing.T) {
	ctx := context.Background()
	e, err := engine.Open(ctx, engine.Options{Path: filepath.Join(t.TempDir(), "l.db"), Summarizer: summarize.Extractive{}, SyncIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	s, _ := e.OpenSession(ctx, "s", engine.SessionConfig{})
	a, _ := s.Agent(ctx, "a")
	a.Append(ctx, msg.Text(msg.RoleUser, "list files; never touch vendor/"))
	tools := ToolFunc(func(ctx context.Context, call msg.Part) (msg.Message, error) {
		if call.ToolName == "broken" {
			return msg.Message{}, errors.New("exploded")
		}
		return msg.ToolResult(call.ToolCallID, call.ToolName, "a.go\nb.go", false, msg.Meta{}), nil
	})
	turns, err := Run(ctx, a, &script{}, tools, engine.AssembleRequest{IncludeMemoryTools: true}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 || !turns[1].Done || len(turns[0].Results) != 3 {
		t.Fatalf("turns: %d", len(turns))
	}
	if !strings.Contains(turns[0].Results[1].Parts[0].Text, "memory_search") {
		t.Fatalf("memory tool not routed to the engine: %s", turns[0].Results[1].Parts[0].Text)
	}
	if !turns[0].Results[2].Parts[0].IsError || !strings.Contains(turns[0].Results[2].Parts[0].Text, "exploded") {
		t.Fatal("tool error not turned into an error result")
	}
}
