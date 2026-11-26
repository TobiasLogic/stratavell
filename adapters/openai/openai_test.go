package openai

import (
	"encoding/json"
	"testing"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
)

func TestRenderAndParse(t *testing.T) {
	cx := &engine.Context{
		System: []engine.SystemBlock{{Text: "a"}, {Text: "b"}},
		Tools:  []msg.ToolDef{{Name: "bash"}},
		Messages: []engine.AMessage{
			{Role: msg.RoleUser, Parts: []msg.Part{{Type: msg.PartText, Text: "hi"}}},
			{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartThinking, Text: "secret"}, {Type: msg.PartToolCall, ToolCallID: "c1", ToolName: "bash", Input: json.RawMessage(`{"command":"ls"}`)}}},
			{Role: msg.RoleTool, Parts: []msg.Part{{Type: msg.PartToolResult, ToolCallID: "c1", Text: "boom", IsError: true}}},
			{Role: msg.RoleUser, Kind: "tail", Parts: []msg.Part{{Type: msg.PartText, Text: "<task_state/>"}}},
		},
	}
	r, err := Render(cx, Options{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Messages) != 5 || r.Messages[0].Role != "system" || *r.Messages[0].Content != "a\n\nb" {
		t.Fatalf("system merge: %+v", r.Messages[0])
	}
	b, _ := json.Marshal(r)
	if json.Valid(b) && string(b) != "" && contains(string(b), "secret") {
		t.Fatal("thinking must never be sent to OpenAI-format providers")
	}
	if r.Messages[3].Role != "tool" || *r.Messages[3].Content != "error: boom" {
		t.Fatalf("tool message: %+v", r.Messages[3])
	}
	bad := *cx
	bad.Messages = cx.Messages[:2]
	if _, err := Render(&bad, Options{}); err == nil {
		bad.Messages = append(bad.Messages, engine.AMessage{Role: msg.RoleUser, Parts: []msg.Part{{Type: msg.PartText, Text: "x"}}})
		if _, err := Render(&bad, Options{}); err == nil {
			t.Fatal("unanswered tool call accepted")
		}
	}
	var resp Response
	json.Unmarshal([]byte(`{"choices":[{"message":{"content":"ok","tool_calls":[{"id":"t","type":"function","function":{"name":"bash","arguments":"{\"a\":1}"}},{"id":"u","type":"function","function":{"name":"bash","arguments":"not json"}}]}}],"usage":{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":80}}}`), &resp)
	m, u, err := Parse(resp)
	if err != nil || len(m.Parts) != 3 || u.InputTokens != 20 || u.CacheReadTokens != 80 {
		t.Fatalf("parse: %+v %+v %v", m, u, err)
	}
	if !json.Valid(m.Parts[2].Input) {
		t.Fatal("invalid arguments must be wrapped into valid JSON")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
