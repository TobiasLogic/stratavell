package server_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestHTTPQueryCloseAndToolSets(t *testing.T) {
	ts, _ := setup(t, "")
	base := "/v1/sessions/q/agents/main"
	if code, _ := call(t, ts, "POST", "/v1/sessions", "", map[string]any{"id": "q"}); code != 200 {
		t.Fatalf("create session: %d", code)
	}
	if code, _ := call(t, ts, "POST", "/v1/sessions/q/agents", "", map[string]any{"id": "main"}); code != 200 {
		t.Fatalf("create agent: %d", code)
	}
	var rows []string
	for i := 0; i < 30; i++ {
		rows = append(rows, fmt.Sprintf(`{"id":%d,"qty":%d}`, i, i%7))
	}
	msgs := []map[string]any{
		{"role": "user", "parts": []map[string]any{{"type": "text", "text": "count low stock"}}},
		{"role": "assistant", "parts": []map[string]any{{"type": "tool_call", "tool_call_id": "c1", "tool_name": "list", "input": map[string]any{}}}},
		{"role": "tool", "parts": []map[string]any{{"type": "tool_result", "tool_call_id": "c1", "tool_name": "list", "text": "[" + strings.Join(rows, ",") + "]"}}},
	}
	code, out := call(t, ts, "POST", base+"/events", "", map[string]any{"messages": msgs})
	if code != 200 {
		t.Fatalf("append: %d %v", code, out)
	}
	ev := out["events"].([]any)[2].(map[string]any)["id"].(string)
	code, out = call(t, ts, "POST", base+"/tools/memory_query", "", map[string]any{"input": map[string]any{"id": ev, "where": map[string]any{"qty": 0}, "fields": []string{"id"}}})
	if code != 200 || out["is_error"] == true || !strings.Contains(out["text"].(string), "5 match") {
		t.Fatalf("memory_query over HTTP: %d %v", code, out)
	}
	_, core := call(t, ts, "POST", base+"/assemble", "", map[string]any{"include_memory_tools": true})
	_, all := call(t, ts, "POST", base+"/assemble", "", map[string]any{"include_memory_tools": true, "all_memory_tools": true})
	if strings.Contains(toJSON(core["context"]), "memory_stats") || !strings.Contains(toJSON(core["context"]), "memory_query") || !strings.Contains(toJSON(all["context"]), "memory_stats") {
		t.Fatal("include_memory_tools should send the core set, and all_memory_tools every tool")
	}
	code, out = call(t, ts, "POST", "/v1/sessions/q/close", "", map[string]any{})
	if code != 200 || out["cancelled_jobs"] == nil {
		t.Fatalf("close: %d %v", code, out)
	}
	code, out = call(t, ts, "POST", base+"/tools/memory_query", "", map[string]any{"input": map[string]any{"id": ev}})
	if code != 200 || out["is_error"] == true {
		t.Fatalf("a closed session reopens on the next request: %d %v", code, out)
	}
}
