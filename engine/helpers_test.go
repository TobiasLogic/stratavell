package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
	"github.com/TobiasLogic/palimpsest/summarize"
)

func smallPolicy(window int) engine.Policy {
	p := engine.DefaultPolicy()
	p.Budget.ContextWindow = window
	p.Budget.MaxOutputTokens = window / 10
	p.Budget.SafetyTokens = 200
	p.Budget.MaxObservationToken = window / 12
	p.Compaction.LeafChunkTokens = window / 8
	p.Compaction.MinChunkTokens = window / 40
	p.Compaction.SummaryMaxTokens = window / 40
	p.Compaction.RetryBackoff = time.Millisecond
	p.Compaction.JobTimeout = 2 * time.Second
	return p
}

type opt func(*engine.Options)

func withSummarizer(s engine.Summarizer) opt { return func(o *engine.Options) { o.Summarizer = s } }
func manual() opt                           { return func(o *engine.Options) { o.Manual = true } }
func withPath(p string) opt                 { return func(o *engine.Options) { o.Path = p } }
func seeded(seed uint64) opt {
	return func(o *engine.Options) {
		c := engine.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		o.Clock = c
		o.IDs = engine.NewSeededIDs(c, seed)
	}
}

func openEngine(t testing.TB, pol engine.Policy, opts ...opt) *engine.Engine {
	t.Helper()
	o := engine.Options{Path: filepath.Join(t.TempDir(), "p.db"), Policy: pol, Summarizer: summarize.Extractive{}, SyncIndex: true}
	for _, f := range opts {
		f(&o)
	}
	e, err := engine.Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func mustAgent(t testing.TB, e *engine.Engine, session, agent string) *engine.Agent {
	t.Helper()
	s, err := e.OpenSession(context.Background(), session, engine.SessionConfig{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Agent(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustAppend(t testing.TB, a *engine.Agent, m msg.Message) engine.EventRef {
	t.Helper()
	r, err := a.Append(context.Background(), m)
	if err != nil {
		t.Fatalf("append %s: %v", m.Role, err)
	}
	return r
}

type simTurn struct {
	user      *msg.Message
	assistant msg.Message
	results   []msg.Message
}

type sim struct {
	r      *rand.Rand
	n      int
	files  map[string]string
	paths  []string
}

func newSim(seed uint64) *sim {
	s := &sim{r: rand.New(rand.NewPCG(seed, seed*7+1)), files: map[string]string{}}
	for i := 0; i < 12; i++ {
		p := fmt.Sprintf("pkg/mod%d/file%d.go", i%4, i)
		s.paths = append(s.paths, p)
		s.files[p] = s.code(p, 40+s.r.IntN(200))
	}
	return s
}

func (s *sim) code(path string, lines int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package mod\n\n")
	for i := 0; i < lines; i++ {
		switch i % 7 {
		case 0:
			fmt.Fprintf(&b, "func Handle%sRequest%d(ctx context.Context) error {\n", strings.ToUpper(path[4:5]), i)
		case 6:
			b.WriteString("}\n")
		default:
			fmt.Fprintf(&b, "\tvalue_%d := compute(%d, %q)\n", i, s.r.IntN(1000), path)
		}
	}
	return b.String()
}

func (s *sim) id() string {
	s.n++
	return fmt.Sprintf("call_%d", s.n)
}

func (s *sim) log(lines int, fail bool) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "=== RUN   TestCase%d\n--- PASS: TestCase%d (0.0%ds)\n", i, i, s.r.IntN(9))
	}
	if fail {
		b.WriteString("--- FAIL: TestParserEdgeCase (0.01s)\n    parser_test.go:88: expected 42, got 41\n")
		b.WriteString("FAIL\tgithub.com/example/pkg/parser\t0.120s\n")
	} else {
		b.WriteString("ok  \tgithub.com/example/pkg/parser\t0.120s\n")
	}
	return b.String()
}

func (s *sim) turn(i int) simTurn {
	var t simTurn
	if i == 0 || s.r.IntN(6) == 0 {
		u := msg.Text(msg.RoleUser, fmt.Sprintf("Step %d: please continue refactoring the parser. Never modify files under vendor/. Keep the public API stable.", i))
		t.user = &u
	}
	ncalls := 1 + s.r.IntN(3)
	a := msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartText, Text: fmt.Sprintf("Working on step %d.", i)}}}
	for c := 0; c < ncalls; c++ {
		id := s.id()
		switch s.r.IntN(5) {
		case 0, 1:
			p := s.paths[s.r.IntN(len(s.paths))]
			in, _ := json.Marshal(map[string]string{"path": p})
			a.Parts = append(a.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: id, ToolName: "read_file", Input: in})
			t.results = append(t.results, msg.ToolResult(id, "read_file", s.files[p], false, msg.Meta{ContentType: msg.CTFileRead, Paths: []string{p}}))
		case 2:
			p := s.paths[s.r.IntN(len(s.paths))]
			s.files[p] = s.code(p, 40+s.r.IntN(200))
			in, _ := json.Marshal(map[string]string{"path": p, "content": s.files[p]})
			a.Parts = append(a.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: id, ToolName: "write_file", Input: in})
			t.results = append(t.results, msg.ToolResult(id, "write_file", "wrote "+p, false, msg.Meta{ContentType: msg.CTFileWrite, Paths: []string{p}}))
		case 3:
			fail := s.r.IntN(2) == 0
			code := 0
			if fail {
				code = 1
			}
			in, _ := json.Marshal(map[string]string{"command": "go test ./..."})
			a.Parts = append(a.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: id, ToolName: "bash", Input: in})
			t.results = append(t.results, msg.ToolResult(id, "bash", s.log(20+s.r.IntN(400), fail), fail, msg.Meta{ContentType: msg.CTTest, Command: "go test ./...", ExitCode: &code}))
		default:
			in, _ := json.Marshal(map[string]string{"command": "grep -rn compute ."})
			a.Parts = append(a.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: id, ToolName: "bash", Input: in})
			var b strings.Builder
			for k := 0; k < 30+s.r.IntN(2000); k++ {
				fmt.Fprintf(&b, "%s:%d: value := compute(%d)\n", s.paths[k%len(s.paths)], k, k)
			}
			code := 0
			t.results = append(t.results, msg.ToolResult(id, "bash", b.String(), false, msg.Meta{ContentType: msg.CTCommand, Command: "grep -rn compute .", ExitCode: &code}))
		}
	}
	t.assistant = a
	return t
}

func runSim(t testing.TB, a *engine.Agent, turns int, seed uint64, each func(i int, c *engine.Context)) []engine.EventRef {
	t.Helper()
	s := newSim(seed)
	ctx := context.Background()
	var refs []engine.EventRef
	for i := 0; i < turns; i++ {
		tr := s.turn(i)
		if tr.user != nil {
			refs = append(refs, mustAppend(t, a, *tr.user))
		}
		c, err := a.Assemble(ctx, engine.AssembleRequest{System: "You are a coding agent.", IncludeMemoryTools: true})
		if err != nil {
			t.Fatalf("turn %d assemble: %v", i, err)
		}
		if each != nil {
			each(i, c)
		}
		refs = append(refs, mustAppend(t, a, tr.assistant))
		for _, r := range tr.results {
			refs = append(refs, mustAppend(t, a, r))
		}
	}
	return refs
}

func contextText(c *engine.Context) string {
	var b strings.Builder
	for _, s := range c.System {
		b.WriteString(s.Text)
	}
	for _, m := range c.Messages {
		for _, p := range m.Parts {
			b.WriteString(p.Text)
			b.Write(p.Input)
		}
	}
	return b.String()
}
