package bench

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"

	"github.com/TobiasLogic/stratavell/msg"
)

type Fact struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Question string   `json:"question"`
	Keys     []string `json:"keys"`
	Query    string   `json:"query"`
	Turn     int      `json:"turn"`
	Standing bool     `json:"standing"`
	Answer   []string `json:"answer_keys,omitempty"`
	Evidence []string `json:"evidence_keys,omitempty"`
}

func (f Fact) GroundKeys() []string {
	return append(append([]string(nil), f.Keys...), f.Evidence...)
}

type Step struct {
	User      *msg.Message
	Assistant msg.Message
	Results   []msg.Message
	Probe     bool
}

type Scenario struct {
	Name  string
	Seed  uint64
	Steps []Step
	Facts []Fact
	live  map[string]*Fact
}

type gen struct {
	r     *rand.Rand
	n     int
	files map[string]string
	order []string
	sc    *Scenario
	fail  int
}

func (g *gen) id() string {
	g.n++
	return fmt.Sprintf("toolu_%04d", g.n)
}

func (g *gen) word() string {
	ws := []string{"index", "cache", "batch", "writer", "reader", "segment", "cursor", "lease", "token", "shard", "replica", "flush", "commit", "snapshot", "ledger", "queue", "worker", "router", "codec", "frame"}
	return ws[g.r.IntN(len(ws))]
}

func (g *gen) codeFile(path string, lines int, plant string) string {
	var b strings.Builder
	pkg := strings.Split(path, "/")[1]
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"context\"\n\t\"errors\"\n\t\"sync\"\n)\n\n", pkg)
	for i := 0; i < lines; {
		name := strings.ToUpper(g.word()[:1]) + g.word() + fmt.Sprint(g.r.IntN(90)+10)
		fmt.Fprintf(&b, "func (s *%sService) %s(ctx context.Context, id string) error {\n", strings.Title(pkg), name)
		body := 3 + g.r.IntN(9)
		for k := 0; k < body; k++ {
			switch g.r.IntN(4) {
			case 0:
				fmt.Fprintf(&b, "\tif err := s.%s.%s(ctx, id); err != nil {\n\t\treturn err\n\t}\n", g.word(), strings.Title(g.word()))
			case 1:
				fmt.Fprintf(&b, "\ts.mu.Lock()\n\ts.%sCount += %d\n\ts.mu.Unlock()\n", g.word(), g.r.IntN(9)+1)
			case 2:
				fmt.Fprintf(&b, "\t// %s %s handling for %s\n", g.word(), g.word(), name)
			default:
				fmt.Fprintf(&b, "\tif id == \"\" {\n\t\treturn errors.New(\"%s: empty id\")\n\t}\n", g.word())
			}
		}
		b.WriteString("\treturn nil\n}\n\n")
		i += body*3 + 3
		if plant != "" && i > lines/2 {
			b.WriteString(plant)
			b.WriteString("\n\n")
			plant = ""
		}
	}
	return b.String()
}

func (g *gen) testLog(tests int, failing []string, errLine string) string {
	var b strings.Builder
	for i := 0; i < tests; i++ {
		name := fmt.Sprintf("Test%s%s%d", strings.Title(g.word()), strings.Title(g.word()), i)
		fmt.Fprintf(&b, "=== RUN   %s\n--- PASS: %s (0.%02ds)\n", name, name, g.r.IntN(99))
		if g.r.IntN(6) == 0 {
			fmt.Fprintf(&b, "    %s_test.go:%d: debug: %s=%d\n", g.word(), 10+g.r.IntN(400), g.word(), g.r.IntN(5000))
		}
	}
	for _, f := range failing {
		fmt.Fprintf(&b, "=== RUN   %s\n--- FAIL: %s (0.04s)\n    %s\n", f, f, errLine)
	}
	if len(failing) > 0 {
		b.WriteString("FAIL\nFAIL\tgithub.com/acme/ledger/pkg/journal\t1.284s\nFAIL\n")
	} else {
		b.WriteString("PASS\nok  \tgithub.com/acme/ledger/pkg/journal\t1.284s\n")
	}
	return b.String()
}

func (g *gen) grep(term string, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		p := g.order[g.r.IntN(len(g.order))]
		fmt.Fprintf(&b, "%s:%d:\ts.%s.%s(ctx, id) // %s\n", p, 1+g.r.IntN(900), term, strings.Title(g.word()), g.word())
	}
	return b.String()
}

func (g *gen) fact(f Fact) {
	f.Turn = len(g.sc.Steps)
	g.sc.Facts = append(g.sc.Facts, f)
}

func (g *gen) call(a *msg.Message, tool string, input map[string]string) string {
	id := g.id()
	in, _ := json.Marshal(input)
	a.Parts = append(a.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: id, ToolName: tool, Input: in})
	return id
}

func (g *gen) read(a *msg.Message, res *[]msg.Message, path string) {
	id := g.call(a, "read_file", map[string]string{"path": path})
	*res = append(*res, msg.ToolResult(id, "read_file", g.files[path], false, msg.Meta{ContentType: msg.CTFileRead, Paths: []string{path}}))
}

func (g *gen) run(a *msg.Message, res *[]msg.Message, cmd, out string, code int) {
	id := g.call(a, "bash", map[string]string{"command": cmd})
	ct := msg.CTCommand
	if strings.Contains(cmd, "test") {
		ct = msg.CTTest
	}
	c := code
	*res = append(*res, msg.ToolResult(id, "bash", out, code != 0, msg.Meta{ContentType: ct, Command: cmd, ExitCode: &c}))
}

func (g *gen) write(a *msg.Message, res *[]msg.Message, path string) {
	g.files[path] = g.codeFile(path, 60+g.r.IntN(240), "")
	id := g.call(a, "write_file", map[string]string{"path": path, "content": g.files[path]})
	*res = append(*res, msg.ToolResult(id, "write_file", fmt.Sprintf("wrote %d bytes to %s", len(g.files[path]), path), false, msg.Meta{ContentType: msg.CTFileWrite, Paths: []string{path}}))
}

func (g *gen) step(user string, think string, build func(a *msg.Message, res *[]msg.Message)) {
	var st Step
	if user != "" {
		u := msg.Text(msg.RoleUser, user)
		st.User = &u
	}
	a := msg.Message{Role: msg.RoleAssistant, Parts: []msg.Part{{Type: msg.PartText, Text: think}}}
	var res []msg.Message
	build(&a, &res)
	st.Assistant = a
	st.Results = res
	g.sc.Steps = append(g.sc.Steps, st)
}

func Generate(seed uint64, turns int) *Scenario {
	g := &gen{r: rand.New(rand.NewPCG(seed, seed^0xa5a5a5a5)), files: map[string]string{}, sc: &Scenario{Name: "coding-session", Seed: seed}}
	pkgs := []string{"journal", "store", "api", "cache", "replica", "auth"}
	for i := 0; i < 36; i++ {
		p := fmt.Sprintf("pkg/%s/%s_%d.go", pkgs[i%len(pkgs)], g.word(), i)
		g.order = append(g.order, p)
		g.files[p] = g.codeFile(p, 80+g.r.IntN(500), "")
	}
	cfg := "pkg/api/config.go"
	g.order = append(g.order, cfg)
	retry := 3 + g.r.IntN(9)
	port := 8000 + g.r.IntN(999)
	g.files[cfg] = g.codeFile(cfg, 240, fmt.Sprintf("const (\n\tRetryLimit = %d\n\tListenPort = %d\n\tLeaseTTLSeconds = %d\n)", retry, port, 30+g.r.IntN(90)))
	writer := "pkg/journal/writer.go"
	g.order = append(g.order, writer)
	batchFn := "flush" + strings.Title(g.word()) + "Batch"
	g.files[writer] = g.codeFile(writer, 400, fmt.Sprintf("func (w *Writer) %s(ctx context.Context, frames [][]byte) error {\n\treturn w.fsyncGroup(ctx, frames)\n}", batchFn))

	g.fact(Fact{ID: "constraint_scope", Kind: "constraint", Question: "Which directory must never be modified in this task?", Keys: []string{"never modify anything under vendor/"}, Answer: []string{"vendor/"}, Query: "never modify directory", Standing: true})
	g.fact(Fact{ID: "constraint_api", Kind: "constraint", Question: "Which package's public API must stay unchanged?", Keys: []string{"public API of package store"}, Answer: []string{"store"}, Query: "public API unchanged package", Standing: true})
	g.step("We are hardening the ledger service. Ground rules for the whole task: never modify anything under vendor/, and keep the public API of package store unchanged. Start by mapping the journal and store packages.", "I'll start by exploring the repository layout.", func(a *msg.Message, res *[]msg.Message) {
		var b strings.Builder
		for _, p := range g.order {
			fmt.Fprintf(&b, "%s\n", p)
		}
		for i := 0; i < 300; i++ {
			fmt.Fprintf(&b, "vendor/github.com/%s/%s/%s_%d.go\n", g.word(), g.word(), g.word(), i)
		}
		g.run(a, res, "find . -name '*.go'", b.String(), 0)
	})
	decisionAt := turns / 8
	rejectAt := turns / 5
	configAt := turns / 6
	processAt := turns / 3
	writerAt := turns / 10
	errSeq := 0
	errLine := ""
	errMsg := ""
	for t := 1; t < turns; t++ {
		switch {
		case t == writerAt:
			g.fact(Fact{ID: "symbol_batch", Kind: "symbol", Question: "Which function in pkg/journal/writer.go performs fsync batching?", Keys: []string{batchFn}, Evidence: []string{"w.fsyncGroup(ctx, frames)"}, Query: "writer.go fsync batching function"})
			g.step("", "Reading the journal writer to understand fsync batching.", func(a *msg.Message, res *[]msg.Message) { g.read(a, res, writer) })
			continue
		case t == configAt:
			g.fact(Fact{ID: "value_retry", Kind: "value", Question: "What value is RetryLimit set to in pkg/api/config.go?", Keys: []string{fmt.Sprintf("RetryLimit = %d", retry)}, Query: "RetryLimit config.go"})
			g.fact(Fact{ID: "value_port", Kind: "value", Question: "Which ListenPort is configured in pkg/api/config.go?", Keys: []string{fmt.Sprintf("ListenPort = %d", port)}, Query: "ListenPort config"})
			g.step("", "Checking the API configuration constants.", func(a *msg.Message, res *[]msg.Message) { g.read(a, res, cfg) })
			continue
		case t == decisionAt:
			g.fact(Fact{ID: "decision_journal", Kind: "decision", Question: "Which storage approach was chosen for the journal, and why?", Keys: []string{"wal", "crash"}, Query: "decision journal storage chosen"})
			g.step("", "Decision: the journal will use SQLite in WAL mode with group commit, because it survives a crash mid-write without needing a separate server. I'll record this before continuing.", func(a *msg.Message, res *[]msg.Message) {
				g.run(a, res, "go doc ./pkg/journal", g.grep("journal", 30), 0)
			})
			continue
		case t == rejectAt:
			g.fact(Fact{ID: "rejected_mutex", Kind: "rejected", Question: "Why was the global-mutex approach for the cache rejected?", Keys: []string{"p99"}, Query: "rejected global mutex cache"})
			g.step("", "Rejected approach: a single global mutex around the cache. It serialized every reader and doubled p99 latency in the benchmark, so I'm switching to sharded locks.", func(a *msg.Message, res *[]msg.Message) {
				g.run(a, res, "go test -bench=. ./pkg/cache", fmt.Sprintf("goos: linux\nBenchmarkGet-8   \t%d\t %d ns/op\nBenchmarkGetParallel-8\t%d\t%d ns/op\nPASS\nok  \tgithub.com/acme/ledger/pkg/cache\t3.1s\n", 1000000+g.r.IntN(500000), 300+g.r.IntN(200), 200000, 2400+g.r.IntN(800)), 0)
			})
			continue
		case t == processAt:
			g.fact(Fact{ID: "process_vet", Kind: "constraint", Question: "What must be run before every commit, per the user's later instruction?", Keys: []string{"go vet ./... before every commit"}, Answer: []string{"go vet"}, Query: "run before every commit", Standing: true})
			g.step("New rule from now on: run go vet ./... before every commit, and paste its output.", "Understood, I'll run go vet before each commit.", func(a *msg.Message, res *[]msg.Message) {
				g.run(a, res, "go vet ./...", "", 0)
			})
			continue
		}
		var user string
		if g.r.IntN(25) == 0 {
			user = fmt.Sprintf("Status check: how is the %s refactor going? Keep going.", g.word())
		}
		kind := g.r.IntN(10)
		g.step(user, fmt.Sprintf("Next: working on the %s %s path (step %d).", g.word(), g.word(), t), func(a *msg.Message, res *[]msg.Message) {
			switch {
			case kind < 3:
				for k := 0; k < 1+g.r.IntN(3); k++ {
					g.read(a, res, g.order[g.r.IntN(len(g.order))])
				}
			case kind < 5:
				g.write(a, res, g.order[g.r.IntN(len(g.order)-2)])
			case kind < 8:
				failing := []string{}
				code := 0
				if g.r.IntN(3) > 0 {
					errSeq++
					line := 100 + g.r.IntN(400)
					errMsg = fmt.Sprintf("replay mismatch after crash: expected %d frames, got %d (case %d)", 10+g.r.IntN(50), g.r.IntN(10), errSeq)
					errLine = fmt.Sprintf("journal_test.go:%d: %s", line, errMsg)
					failing = []string{fmt.Sprintf("TestReplayAfterCrash%d", errSeq%7)}
					code = 1
					g.fail = errSeq
				} else {
					g.fail = 0
				}
				g.run(a, res, "go test ./pkg/journal/...", g.testLog(60+g.r.IntN(900), failing, errLine), code)
			default:
				g.run(a, res, "grep -rn "+g.word()+" pkg/", g.grep(g.word(), 40+g.r.IntN(1500)), 0)
			}
		})
		if g.fail > 0 {
			g.sc.live = map[string]*Fact{"error": {ID: fmt.Sprintf("error_current_%d", g.fail), Kind: "error", Question: "What is the exact assertion message of the currently failing test?", Keys: []string{errLine}, Answer: []string{errMsg}, Query: "currently failing test replay mismatch"}}
		} else {
			g.sc.live = nil
		}
		if t%(max(1, turns/12)) == 0 {
			g.sc.Steps[len(g.sc.Steps)-1].Probe = true
			if f, ok := g.sc.live["error"]; ok {
				ff := *f
				ff.Turn = len(g.sc.Steps) - 1
				g.sc.Facts = append(g.sc.Facts, ff)
			}
		}
	}
	return g.sc
}

func (s *Scenario) FactsAt(turn int) []Fact {
	var out []Fact
	latestErr := -1
	for i, f := range s.Facts {
		if f.Turn > turn {
			continue
		}
		if f.Kind == "error" {
			if f.Turn == turn {
				latestErr = i
			}
			continue
		}
		out = append(out, f)
	}
	if latestErr >= 0 {
		out = append(out, s.Facts[latestErr])
	}
	return out
}

func (s *Scenario) RawTokens(count func(string) int) int {
	n := 0
	for _, st := range s.Steps {
		if st.User != nil {
			n += count(st.User.PlainText())
		}
		n += count(st.Assistant.PlainText())
		for _, r := range st.Results {
			n += count(r.PlainText())
		}
	}
	return n
}
