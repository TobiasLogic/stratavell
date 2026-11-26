package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/server"
	"github.com/TobiasLogic/stratavell/summarize"
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

func TestServeRefusesToReplaceNonSocket(t *testing.T) {
	e, err := engine.Open(context.Background(), engine.Options{Path: filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	victim := filepath.Join(t.TempDir(), "important.db")
	if err := os.WriteFile(victim, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = server.New(e, "").Serve(context.Background(), server.Listeners{Socket: victim})
	if err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("want refusal, got %v", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me" {
		t.Fatal("existing file was modified")
	}
}

func TestLoopbackGuardWithoutToken(t *testing.T) {
	e, err := engine.Open(context.Background(), engine.Options{Path: filepath.Join(t.TempDir(), "s.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.New(e, "").Serve(ctx, server.Listeners{Addr: addr}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	get := func(host, origin string) int {
		for i := 0; i < 50; i++ {
			req, _ := http.NewRequest("GET", "http://"+addr+"/v1/stats", nil)
			if host != "" {
				req.Host = host
			}
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			resp.Body.Close()
			return resp.StatusCode
		}
		t.Fatal("server did not start")
		return 0
	}
	if c := get("", ""); c != 200 {
		t.Fatalf("plain loopback request: %d", c)
	}
	if c := get("localhost:7077", "http://localhost:3000"); c != 200 {
		t.Fatalf("loopback origin: %d", c)
	}
	if c := get("attacker.example:7077", ""); c != 403 {
		t.Fatalf("rebinding host accepted: %d", c)
	}
	if c := get("", "https://attacker.example"); c != 403 {
		t.Fatalf("foreign origin accepted: %d", c)
	}
}
