package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
)

func sample() *engine.Context {
	return &engine.Context{
		System: []engine.SystemBlock{{Text: "host"}, {Text: "engine notice"}},
		Tools:  []msg.ToolDef{{Name: "bash", Schema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []engine.AMessage{
			{Role: msg.RoleUser, Kind: "archive", Parts: []msg.Part{{Type: msg.PartText, Text: "<archive>..</archive>"}, {Type: msg.PartText, Text: "do the thing"}}},
			{Role: msg.RoleAssistant, Kind: "history", KeepThinking: false, Parts: []msg.Part{{Type: msg.PartThinking, Text: "old", Signature: "s0"}, {Type: msg.PartToolCall, ToolCallID: "a", ToolName: "bash", Input: json.RawMessage(`{"command":"ls"}`)}}},
			{Role: msg.RoleTool, Kind: "history", Parts: []msg.Part{{Type: msg.PartToolResult, ToolCallID: "a", Text: "x"}}},
			{Role: msg.RoleAssistant, Kind: "history", KeepThinking: true, Parts: []msg.Part{{Type: msg.PartThinking, Text: "new", Signature: "s1"}, {Type: msg.PartToolCall, ToolCallID: "b", ToolName: "bash", Input: json.RawMessage(`{"command":"pwd"}`)}, {Type: msg.PartToolCall, ToolCallID: "c", ToolName: "bash", Input: json.RawMessage(`{}`)}}},
			{Role: msg.RoleTool, Kind: "history", Parts: []msg.Part{{Type: msg.PartToolResult, ToolCallID: "b", Text: "/"}}},
			{Role: msg.RoleTool, Kind: "history", Parts: []msg.Part{{Type: msg.PartToolResult, ToolCallID: "c", Text: "", IsError: true}}},
			{Role: msg.RoleUser, Kind: "tail", Parts: []msg.Part{{Type: msg.PartText, Text: "<task_state/>"}}},
		},
		Breakpoints: []engine.Breakpoint{{Message: -1, Part: 1, Label: "system"}, {Message: 0, Part: 0, Label: "archive"}, {Message: 5, Part: 0, Label: "history"}, {Message: 3, Part: 0, Label: "prev"}, {Message: 1, Part: 1}},
	}
}

func TestRenderMergesAndPlacesBreakpoints(t *testing.T) {
	r, err := Render(sample(), Options{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Messages) != 5 {
		b, _ := json.MarshalIndent(r.Messages, "", " ")
		t.Fatalf("expected 5 wire messages, got %d\n%s", len(r.Messages), b)
	}
	last := r.Messages[4]
	if last.Role != "user" || last.Content[0].Type != "tool_result" || last.Content[1].Type != "tool_result" || last.Content[2].Type != "text" {
		t.Fatalf("tool results and tail not merged in order: %+v", last.Content)
	}
	for _, b := range r.Messages[1].Content {
		if b.Type == "thinking" {
			t.Fatal("stale thinking kept")
		}
	}
	if r.Messages[3].Content[0].Type != "thinking" || r.Messages[3].Content[0].Signature != "s1" {
		t.Fatal("current thinking dropped or altered")
	}
	n := 0
	for _, m := range r.Messages {
		for _, b := range m.Content {
			if b.CacheControl != nil {
				n++
				if b.Type == "thinking" {
					t.Fatal("cache_control on thinking block")
				}
			}
		}
	}
	if r.System[1].CacheControl == nil || n+1 > 4 {
		t.Fatalf("breakpoints wrong: system=%v messages=%d", r.System[1].CacheControl, n)
	}
	if r.Messages[4].Content[1].Content != "(no output)" {
		t.Fatal("empty tool result must get placeholder text")
	}
}

func TestRenderRejectsBrokenPairing(t *testing.T) {
	c := sample()
	c.Messages = c.Messages[:4]
	c.Breakpoints = nil
	if _, err := Render(c, Options{}); err == nil || !strings.Contains(err.Error(), "tool_use") {
		t.Fatalf("expected pairing error, got %v", err)
	}
}

func TestParseRoundTrip(t *testing.T) {
	raw := `{"id":"msg_1","model":"m","content":[{"type":"thinking","thinking":"t","signature":"sig"},{"type":"redacted_thinking","data":"enc"},{"type":"text","text":"hi"},{"type":"tool_use","id":"tu","name":"bash","input":{"b":1,"a":2}},{"type":"server_tool_use","id":"s","name":"web_search","input":{}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":7}}`
	var resp Response
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatal(err)
	}
	m, u := Parse(resp)
	if u.TotalInput() != 117 || u.CacheReadTokens != 100 {
		t.Fatalf("usage %+v", u)
	}
	if string(m.Parts[3].Input) != `{"b":1,"a":2}` {
		t.Fatalf("tool input key order changed: %s", m.Parts[3].Input)
	}
	if m.Parts[4].Type != msg.PartOpaque {
		t.Fatal("unknown block not preserved as opaque")
	}
	b, _ := block(m.Parts[1], true)
	if b.Data != "enc" {
		t.Fatal("redacted thinking lost")
	}
}
