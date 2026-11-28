package openai_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TobiasLogic/stratavell/adapters/openai"
	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
	"github.com/TobiasLogic/stratavell/summarize"
)

func TestSummaryRequestsCarryJobTags(t *testing.T) {
	var job, attempt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		job, attempt = r.Header.Get("X-Stratavell-Job"), r.Header.Get("X-Stratavell-Attempt")
		w.Header().Set("content-type", "application/json")
		w.Write([]byte(`{"model":"m","choices":[{"message":{"role":"assistant","content":"{\"summary\":\"s\"}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer srv.Close()
	c := openai.NewClient(srv.URL, "m")
	c.APIKey = ""
	sum := summarize.LLM{Model: c}
	ctx := engine.WithJobTag(context.Background(), engine.JobTag{ID: "jb_1", Attempt: 2})
	if _, err := sum.Summarize(ctx, engine.SummarizeInput{Items: []engine.SummaryInputItem{{ID: "ev_1", Role: string(msg.RoleUser), Text: "x"}}}); err != nil {
		t.Fatal(err)
	}
	if job != "jb_1" || attempt != "2" {
		t.Fatalf("headers: job %q attempt %q", job, attempt)
	}
	if _, err := c.Complete(context.Background(), "s", "u", 10); err != nil {
		t.Fatal(err)
	}
	if job != "" {
		t.Fatalf("a request outside a job must not be tagged, got %q", job)
	}
}
