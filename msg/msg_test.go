package msg

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestPairing(t *testing.T) {
	seq := []Message{
		Text(RoleUser, "hi"),
		{Role: RoleAssistant, Parts: []Part{{Type: PartToolCall, ToolCallID: "a", ToolName: "x"}, {Type: PartToolCall, ToolCallID: "b", ToolName: "x"}}},
		ToolResult("b", "x", "1", false, Meta{}),
		ToolResult("a", "x", "2", false, Meta{}),
		Text(RoleUser, "next"),
	}
	if err := ValidateSequence(seq); err != nil {
		t.Fatal(err)
	}
	bad := append(append([]Message{}, seq[:3]...), Text(RoleUser, "too early"))
	if err := ValidateSequence(bad); !errors.Is(err, ErrUnansweredCall) {
		t.Fatalf("got %v", err)
	}
	dup := []Message{{Role: RoleAssistant, Parts: []Part{{Type: PartToolCall, ToolCallID: "a", ToolName: "x"}, {Type: PartToolCall, ToolCallID: "a", ToolName: "x"}}}}
	if err := ValidateSequence(dup); !errors.Is(err, ErrDuplicateCallID) {
		t.Fatalf("got %v", err)
	}
}

func FuzzMessage(f *testing.F) {
	f.Add([]byte(`{"role":"tool","parts":[{"type":"tool_result","tool_call_id":"x","text":"y"}]}`))
	f.Fuzz(func(t *testing.T, b []byte) {
		var m Message
		if json.Unmarshal(b, &m) != nil {
			return
		}
		_ = m.Validate()
		_ = m.PlainText()
		st := NewPairingState()
		_ = st.Step(m)
		_ = st.Clone().Open()
	})
}
