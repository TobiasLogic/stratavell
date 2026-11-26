package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/server"
	"github.com/TobiasLogic/palimpsest/summarize"
)

func setup(t *testing.T, token string) (*httptest.Server, *engine.Engine) {
	e, err := engine.Open(context.Background(), engine.Options{Path: filepath.Join(t.TempDir(), "s.db"), Summarizer: summarize.Extractive{}, SyncIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.New(e, token))
	t.Cleanup(func() { ts.Close(); e.Close() })
	return ts, e
}

func call(t *testing.T, ts *httptest.Server, method, path, token string, body any) (int, map[string]any) {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, ts.URL+path, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestHTTPFlow(t *testing.T) {
	ts, _ := setup(t, "secret")
	if code, _ := call(t, ts, "POST", "/v1/sessions", "", map[string]string{"id": "s"}); code != 401 {
		t.Fatalf("auth not enforced: %d", code)
	}
	tok := "secret"
	if code, out := call(t, ts, "POST", "/v1/sessions", tok, map[string]string{"id": "s"}); code != 200 {
		t.Fatalf("create session %d %v", code, out)
	}
	call(t, ts, "POST", "/v1/sessions/s/agents", tok, map[string]string{"id": "main"})
	msgs := []map[string]any{
		{"role": "user", "parts": []map[string]any{{"type": "text", "text": "Fix the flaky test. Never edit vendor/."}}},
		{"role": "assistant", "parts": []map[string]any{{"type": "tool_call", "tool_call_id": "c1", "tool_name": "bash", "input": map[string]string{"command": "go test ./..."}}}},
		{"role": "tool", "meta": map[string]any{"exit_code": 1, "command": "go test ./..."}, "parts": []map[string]any{{"type": "tool_result", "tool_call_id": "c1", "text": "--- FAIL: TestFlaky (0.01s)\n    flaky_test.go:12: timeout waiting for lock\nFAIL"}}},
	}
	code, out := call(t, ts, "POST", "/v1/sessions/s/agents/main/events", tok, map[string]any{"messages": msgs})
	if code != 200 {
		t.Fatalf("append %d %v", code, out)
	}
	evs := out["events"].([]any)
	eid := evs[2].(map[string]any)["id"].(string)
	code, out = call(t, ts, "POST", "/v1/sessions/s/agents/main/assemble", tok, map[string]any{"system": "sys", "include_memory_tools": true, "format": "anthropic", "model": "claude-test", "max_tokens": 1000})
	if code != 200 || out["request"] == nil {
		t.Fatalf("assemble %d %v", code, out)
	}
	b, _ := json.Marshal(out["request"])
	if !strings.Contains(string(b), "cache_control") || !strings.Contains(string(b), "tool_result") {
		t.Fatalf("anthropic request missing parts: %s", b)
	}
	code, out = call(t, ts, "POST", "/v1/sessions/s/agents/main/assemble", tok, map[string]any{"format": "openai", "model": "x"})
	if code != 200 {
		t.Fatalf("openai assemble %d %v", code, out)
	}
	code, out = call(t, ts, "GET", "/v1/sessions/s/agents/main/search?q=flaky+lock+timeout", tok, nil)
	if code != 200 || len(out["hits"].([]any)) == 0 {
		t.Fatalf("search %d %v", code, out)
	}
	code, out = call(t, ts, "GET", "/v1/sessions/s/agents/main/events/"+eid, tok, nil)
	if code != 200 || !strings.Contains(toJSON(out), "timeout waiting for lock") {
		t.Fatalf("get event %d %v", code, out)
	}
	code, out = call(t, ts, "POST", "/v1/sessions/s/agents/main/tools/memory_get_task_state", tok, map[string]any{"input": map[string]any{}})
	if code != 200 || !strings.Contains(out["text"].(string), "failure") {
		t.Fatalf("task state tool %d %v", code, out)
	}
	if code, _ = call(t, ts, "GET", "/v1/sessions/nope/agents/main/stats", tok, nil); code != 404 {
		t.Fatalf("unknown session should be 404, got %d", code)
	}
	call(t, ts, "POST", "/v1/sessions", tok, map[string]string{"id": "other"})
	call(t, ts, "POST", "/v1/sessions/other/agents", tok, map[string]string{"id": "main"})
	code, _ = call(t, ts, "GET", "/v1/sessions/other/agents/main/events/"+eid, tok, nil)
	if code != 404 {
		t.Fatalf("cross-session event visible: %d", code)
	}
	bad := []map[string]any{{"role": "tool", "parts": []map[string]any{{"type": "tool_result", "tool_call_id": "zzz", "text": "x"}}}}
	code, out = call(t, ts, "POST", "/v1/sessions/s/agents/main/events", tok, map[string]any{"messages": bad})
	if code != 400 || out["code"] != "protocol_violation" {
		t.Fatalf("orphan result accepted: %d %v", code, out)
	}
	code, out = call(t, ts, "GET", "/v1/sessions/s/verify", tok, nil)
	if code != 200 || out["sqlite_integrity"] != "ok" {
		t.Fatalf("verify %d %v", code, out)
	}
	if code, _ = call(t, ts, "DELETE", "/v1/sessions/s", tok, nil); code != 200 {
		t.Fatalf("delete %d", code)
	}
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
