package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/TobiasLogic/palimpsest/adapters/anthropic"
	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
	"github.com/TobiasLogic/palimpsest/summarize"
)

func main() {
	ctx := context.Background()
	dir, _ := os.MkdirTemp("", "palimpsest-example-")
	defer os.RemoveAll(dir)

	e, err := engine.Open(ctx, engine.Options{
		Path:       filepath.Join(dir, "memory.db"),
		Summarizer: summarize.Extractive{},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer e.Close()

	sess, _ := e.OpenSession(ctx, "demo", engine.SessionConfig{Project: "example"})
	agent, _ := sess.Agent(ctx, "main")

	agent.Append(ctx, msg.Text(msg.RoleUser, "Fix the failing test. Never modify vendor/."))
	agent.Append(ctx, msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{
		{Type: msg.PartText, Text: "Running the tests first."},
		{Type: msg.PartToolCall, ToolCallID: "call_1", ToolName: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`)},
	}})
	exit := 1
	agent.Append(ctx, msg.ToolResult("call_1", "bash", "--- FAIL: TestParse (0.01s)\n    parse_test.go:42: expected 3, got 4\nFAIL", true,
		msg.Meta{Command: "go test ./...", ExitCode: &exit}))

	cx, err := agent.Assemble(ctx, engine.AssembleRequest{System: "You are a coding agent.", IncludeMemoryTools: true})
	if err != nil {
		log.Fatal(err)
	}
	req, err := anthropic.Render(cx, anthropic.Options{Model: "claude-sonnet-5-5", MaxTokens: 4096})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("context: %d tokens of %d, %d messages, %d cache breakpoints, state %s\n", cx.Tokens, cx.Capacity, len(req.Messages), len(cx.Breakpoints), cx.Report.State)

	out := agent.HandleTool(ctx, "memory_get_task_state", nil)
	fmt.Println(out.Text)

	hits, _ := agent.Search(ctx, "failing parse test", engine.SearchFilters{}, 3)
	for _, h := range hits {
		fmt.Printf("hit %s (%s): %s\n", h.ID, h.Provenance, h.Snippet)
	}
}
