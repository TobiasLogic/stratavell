package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
)

func warm(b *testing.B, window, turns int) (*engine.Engine, *engine.Agent) {
	pol := smallPolicy(window)
	e := openEngine(b, pol, manual())
	a := mustAgent(b, e, "s", "a")
	runSim(b, a, turns, 3, func(i int, c *engine.Context) { e.RunJobs(context.Background()) })
	return e, a
}

func BenchmarkAppendSmallEvent(b *testing.B) {
	_, a := warm(b, 200000, 150)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench_%d", i)
		in, _ := json.Marshal(map[string]string{"command": "ls"})
		if _, err := a.Append(ctx, msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartText, Text: "checking"}, {Type: msg.PartToolCall, ToolCallID: id, ToolName: "bash", Input: in}}}); err != nil {
			b.Fatal(err)
		}
		code := 0
		if _, err := a.Append(ctx, msg.ToolResult(id, "bash", "a.go\nb.go\nc.go", false, msg.Meta{ExitCode: &code})); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(2, "events/op")
}

func BenchmarkAssembleSteadyState(b *testing.B) {
	for _, window := range []int{64000, 200000} {
		b.Run(fmt.Sprintf("window=%d", window), func(b *testing.B) {
			e, a := warm(b, window, 150)
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				c, err := a.Assemble(ctx, engine.AssembleRequest{System: "sys", IncludeMemoryTools: true})
				if err != nil {
					b.Fatal(err)
				}
				if i == 0 {
					b.ReportMetric(float64(c.Tokens), "ctx_tokens")
					b.ReportMetric(float64(len(c.Messages)), "messages")
				}
			}
			_ = e
		})
	}
}

func BenchmarkSearch(b *testing.B) {
	e, a := warm(b, 64000, 400)
	var docs int
	e.DB().R.QueryRow("SELECT COUNT(*) FROM docs").Scan(&docs)
	ctx := context.Background()
	queries := []string{"replay mismatch after crash", "HandleRequest compute value", "pkg/mod1/file1.go", "go test failure parser", "never modify vendor"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.Search(ctx, queries[i%len(queries)], engine.SearchFilters{}, 8); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(docs), "indexed_chunks")
	_ = strings.TrimSpace
}

func TestSearchLatencyProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("latency profile")
	}
	for _, turns := range []int{300, 1200} {
		pol := smallPolicy(64000)
		e := openEngine(t, pol, manual())
		a := mustAgent(t, e, "s", "a")
		runSim(t, a, turns, 9, func(i int, c *engine.Context) { e.RunJobs(context.Background()) })
		var docs int
		e.DB().R.QueryRow("SELECT COUNT(*) FROM docs").Scan(&docs)
		queries := []string{"replay mismatch after crash", "HandleRequest compute value", "pkg/mod1/file1.go", "go test failure parser", "never modify vendor", "why was the cache approach rejected", "value_42 compute", "which test is failing right now"}
		var ms []float64
		for i := 0; i < 200; i++ {
			t0 := time.Now()
			if _, err := a.Search(context.Background(), queries[i%len(queries)], engine.SearchFilters{}, 8); err != nil {
				t.Fatal(err)
			}
			ms = append(ms, float64(time.Since(t0).Microseconds())/1000)
		}
		sort.Float64s(ms)
		t.Logf("turns=%d chunks=%d search p50=%.2fms p95=%.2fms p99=%.2fms max=%.2fms", turns, docs, ms[100], ms[190], ms[198], ms[199])
	}
}

func TestMultiAgentLoadProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("load profile")
	}
	pol := smallPolicy(64000)
	e := openEngine(t, pol, func(o *engine.Options) { o.SyncIndex = false })
	ctx := context.Background()
	s, _ := e.OpenSession(ctx, "load", engine.SessionConfig{})
	lead, _ := s.Agent(ctx, "lead")
	mustAppend(t, lead, msg.Text(msg.RoleUser, "coordinate four workers"))
	const workers, turns = 4, 150
	type sample struct{ appendMS, assembleMS []float64 }
	results := make([]sample, workers)
	errs := make(chan error, workers)
	start := time.Now()
	done := make(chan int, workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			a, err := s.Spawn(ctx, lead, fmt.Sprintf("w%d", w), engine.Handoff{Task: fmt.Sprintf("part %d", w)})
			if err != nil {
				errs <- err
				return
			}
			sm := newSim(uint64(500 + w))
			marker := fmt.Sprintf("private-marker-%d-zq", w)
			mustAppend(t, a, msg.Text(msg.RoleUser, marker))
			for i := 0; i < turns; i++ {
				tr := sm.turn(i)
				t0 := time.Now()
				c, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: true})
				if err != nil {
					errs <- err
					return
				}
				results[w].assembleMS = append(results[w].assembleMS, float64(time.Since(t0).Microseconds())/1000)
				if c.Tokens > c.Capacity {
					errs <- fmt.Errorf("overflow")
					return
				}
				for _, m := range append([]msg.Message{tr.assistant}, tr.results...) {
					t1 := time.Now()
					if _, err := a.Append(ctx, m); err != nil {
						errs <- err
						return
					}
					results[w].appendMS = append(results[w].appendMS, float64(time.Since(t1).Microseconds())/1000)
				}
			}
			for o := 0; o < workers; o++ {
				if o == w {
					continue
				}
				hits, _ := a.Search(ctx, fmt.Sprintf("private-marker-%d-zq", o), engine.SearchFilters{}, 5)
				for _, h := range hits {
					if strings.Contains(h.Snippet, fmt.Sprintf("private-marker-%d-zq", o)) {
						errs <- fmt.Errorf("worker %d saw worker %d's private event", w, o)
						return
					}
				}
			}
			done <- w
		}(w)
	}
	for i := 0; i < workers; i++ {
		select {
		case err := <-errs:
			t.Fatal(err)
		case <-done:
		}
	}
	elapsed := time.Since(start)
	var ap, as []float64
	for _, r := range results {
		ap = append(ap, r.appendMS...)
		as = append(as, r.assembleMS...)
	}
	sort.Float64s(ap)
	sort.Float64s(as)
	q := func(xs []float64, p float64) float64 { return xs[int(float64(len(xs)-1)*p)] }
	rep, _ := e.Verify(ctx, "load")
	if len(rep.Problems) > 0 {
		t.Fatal(rep.Problems)
	}
	snap := e.Metrics().Snapshot()
	t.Logf("agents=%d turns=%d elapsed=%.1fs append p50=%.2fms p95=%.2fms p99=%.2fms assemble p50=%.2fms p95=%.2fms p99=%.2fms events=%d summaries_published=%.0f jobs=%.0f",
		workers, turns, elapsed.Seconds(), q(ap, .5), q(ap, .95), q(ap, .99), q(as, .5), q(as, .95), q(as, .99), rep.Events, snap.Counters["summaries_published"], snap.Counters["jobs_scheduled"])
}
