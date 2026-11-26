package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/internal/tokens"
)

type Config struct {
	Window      int
	MaxOutput   int
	Turns       int
	Seeds       []uint64
	Strategies  []string
	OutDir      string
	Summarizer  engine.Summarizer
	SumName     string
	RealTiming  bool
	TurnSeconds float64
	Prices      Prices
	Latency     LatencyModel
	Log         func(string, ...any)
}

func basePolicy(window, maxOut int) engine.Policy {
	p := engine.DefaultPolicy()
	p.Budget.ContextWindow = window
	p.Budget.MaxOutputTokens = maxOut
	p.Budget.MaxObservationToken = max(2000, window/20)
	p.Compaction.LeafChunkTokens = max(4000, window/8)
	p.Compaction.MinChunkTokens = max(1000, window/50)
	p.Compaction.SummaryMaxTokens = max(600, window/50)
	p.Compaction.RetryBackoff = engine.Duration(time.Millisecond)
	return p
}

func bare(p engine.Policy) engine.Policy {
	p.Compaction.Enabled = false
	p.Compaction.Emergency = false
	p.Masking.Mode = engine.MaskOff
	p.Masking.Externalize = false
	p.Masking.Dedupe = false
	p.Masking.UnchangedReads = false
	p.Masking.MaskInputs = false
	p.Retrieval.Enabled = false
	p.Retrieval.AutoInject = false
	p.TaskState = false
	p.PinDirective = false
	return p
}

var AllStrategies = []string{"A-uncompressed", "B-conventional", "C-noa", "D-masking", "E-sync-summary", "F-async-progressive", "G-hybrid"}

var Matched = []string{"B-conventional+pins", "B-conventional+archive", "E-sync-summary+retrieval", "F-async-progressive+retrieval", "G-hybrid"}

var Ablations = []string{"G-no-masking", "G-no-externalize", "G-no-dedupe", "G-no-taskstate", "G-no-pins", "G-no-taskstate-pins", "G-no-retrieval", "G-rolling-mask", "G-layout-spec", "G-sync"}

func PolicyFor(name string, window, maxOut int) (engine.Policy, bool) {
	b := basePolicy(window, maxOut)
	switch name {
	case "A-uncompressed":
		return bare(b), true
	case "D-masking":
		p := bare(b)
		p.Masking.Mode = engine.MaskRolling
		p.Masking.RollingKeep = 10
		return p, true
	case "E-sync-summary":
		p := bare(b)
		p.Compaction.Enabled = true
		p.Compaction.Emergency = true
		p.Compaction.Async = false
		return p, true
	case "F-async-progressive":
		p := bare(b)
		p.Compaction.Enabled = true
		p.Compaction.Emergency = true
		p.Compaction.Async = true
		return p, true
	case "E-sync-summary+retrieval", "F-async-progressive+retrieval":
		p := bare(b)
		p.Compaction.Enabled = true
		p.Compaction.Emergency = true
		p.Compaction.Async = name == "F-async-progressive+retrieval"
		p.Retrieval.Enabled = true
		p.Retrieval.AutoInject = true
		return p, true
	case "G-hybrid":
		return b, true
	case "G-no-masking":
		b.Masking.Mode = engine.MaskOff
		return b, true
	case "G-no-externalize":
		b.Masking.Externalize = false
		return b, true
	case "G-no-dedupe":
		b.Masking.Dedupe = false
		b.Masking.UnchangedReads = false
		return b, true
	case "G-no-taskstate":
		b.TaskState = false
		return b, true
	case "G-no-pins":
		b.PinDirective = false
		return b, true
	case "G-no-taskstate-pins":
		b.TaskState = false
		b.PinDirective = false
		return b, true
	case "G-no-retrieval":
		b.Retrieval.Enabled = false
		b.Retrieval.AutoInject = false
		return b, true
	case "G-rolling-mask":
		b.Masking.Mode = engine.MaskRolling
		return b, true
	case "G-layout-spec":
		b.Cache.Layout = engine.LayoutSpec
		return b, true
	case "G-sync":
		b.Compaction.Async = false
		return b, true
	}
	return b, false
}

type TurnRecord struct {
	Strategy   string  `json:"strategy"`
	Seed       uint64  `json:"seed"`
	Turn       int     `json:"turn"`
	Tokens     int     `json:"tokens"`
	Overflow   bool    `json:"overflow"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Uncached   int     `json:"uncached"`
	CostUSD    float64 `json:"cost_usd"`
	FgMS       float64 `json:"foreground_wait_ms"`
	EngineMS   float64 `json:"engine_ms"`
	ToolTokens int     `json:"tool_tokens"`
	Standing   int     `json:"standing_total"`
	StandingOK int     `json:"standing_visible"`
	State      string  `json:"state,omitempty"`
}

type ProbeRecord struct {
	Strategy  string `json:"strategy"`
	Seed      uint64 `json:"seed"`
	Turn      int    `json:"turn"`
	Fact      string `json:"fact"`
	Kind      string `json:"kind"`
	Visible   bool   `json:"visible"`
	Grounded  bool   `json:"grounded"`
	Retrieved bool   `json:"retrieved"`
	Cost      int    `json:"retrieval_tokens"`
}

type RunSummary struct {
	Strategy          string             `json:"strategy"`
	Seed              uint64             `json:"seed"`
	Turns             int                `json:"turns"`
	RawTokens         int                `json:"raw_session_tokens"`
	InputTokens       int64              `json:"input_tokens"`
	MaxContext        int                `json:"max_context"`
	MeanContext       float64            `json:"mean_context"`
	Overflows         int                `json:"overflow_turns"`
	FirstOverflow     int                `json:"first_overflow_turn"`
	CacheHitRate      float64            `json:"cache_hit_rate"`
	CostUSD           float64            `json:"input_cost_usd"`
	ExtraCalls        int                `json:"extra_primary_calls"`
	ExtraInput        int64              `json:"extra_primary_input_tokens"`
	FgWaitMS          float64            `json:"foreground_wait_ms"`
	EngineP50         float64            `json:"engine_ms_p50"`
	EngineP95         float64            `json:"engine_ms_p95"`
	ToolTokens        int64              `json:"tool_tokens_sent"`
	ProbeVisible      float64            `json:"probe_visible"`
	ProbeRecoverable  float64            `json:"probe_recoverable"`
	Probes            int                `json:"probes"`
	ProbeGrounded     float64            `json:"probe_grounded"`
	CacheReadTokens   int64              `json:"cache_read_tokens"`
	CacheWriteTokens  int64              `json:"cache_write_tokens"`
	UncachedTokens    int64              `json:"uncached_tokens"`
	ExtraOutput       int64              `json:"extra_primary_output_tokens"`
	OutputCostUSD     float64            `json:"extra_output_cost_usd"`
	SummarizerCostUSD float64            `json:"summarizer_cost_usd_at_haiku"`
	TotalCostUSD      float64            `json:"total_cost_usd"`
	CompletionRate    float64            `json:"completion_rate"`
	StandingRetention float64            `json:"standing_retention"`
	ExactRecovery     float64            `json:"exact_recovery"`
	Extra             map[string]float64 `json:"extra"`
	WallSeconds       float64            `json:"wall_seconds"`
	ByKind            map[string][2]int  `json:"probe_by_kind"`
}

func (c *Config) make(ctx context.Context, name string, dir string, seed uint64, start time.Time) (Strategy, error) {
	capacity := basePolicy(c.Window, c.MaxOutput).EffectiveCapacity()
	switch name {
	case "B-conventional":
		return NewConventional(c.Summarizer, capacity, c.Latency, c.RealTiming), nil
	case "B-conventional+pins", "B-conventional+archive":
		cv := NewConventional(c.Summarizer, capacity, c.Latency, c.RealTiming).(*conventional)
		cv.name = name
		if name == "B-conventional+pins" {
			cv.pins = true
		} else {
			ap := bare(basePolicy(c.Window, c.MaxOutput))
			ap.Retrieval.Enabled = true
			arc, err := NewEngineStrategy(ctx, EngineConfig{Name: name + "-archive", Policy: ap, Summarizer: c.Summarizer, Dir: dir, Seed: seed, Latency: c.Latency}, start)
			if err != nil {
				return nil, err
			}
			cv.archive = arc
			cv.memTools = true
		}
		return cv, nil
	case "C-noa":
		return NewNoa(c.Summarizer, capacity, c.Latency, c.RealTiming), nil
	}
	pol, ok := PolicyFor(name, c.Window, c.MaxOutput)
	if !ok {
		return nil, fmt.Errorf("unknown strategy %q", name)
	}
	return NewEngineStrategy(ctx, EngineConfig{Name: name, Policy: pol, Summarizer: c.Summarizer, RealTiming: c.RealTiming, Dir: dir, Seed: seed, Latency: c.Latency}, start)
}

func percentile(xs []float64, q float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	c := append([]float64(nil), xs...)
	sort.Float64s(c)
	i := int(math.Ceil(q*float64(len(c)))) - 1
	return c[max(0, min(i, len(c)-1))]
}

func (c *Config) RunOne(ctx context.Context, name string, sc *Scenario, turnsOut, probesOut *json.Encoder) (RunSummary, error) {
	dir, err := os.MkdirTemp(c.OutDir, "run-")
	if err != nil {
		return RunSummary{}, err
	}
	defer os.RemoveAll(dir)
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	st, err := c.make(ctx, name, dir, sc.Seed, start)
	if err != nil {
		return RunSummary{}, err
	}
	defer st.Close()
	sim := NewCacheSim(c.Prices.MinCache)
	sum := RunSummary{Strategy: name, Seed: sc.Seed, Turns: len(sc.Steps), RawTokens: sc.RawTokens(tokens.Estimate), FirstOverflow: -1, ByKind: map[string][2]int{}}
	var engMS []float64
	var ctxSum float64
	var reads, totalIn int64
	var visible, grounded, recoverable, probes, standing, standingOK int
	var tRead, tWrite, tUnc int64
	wall := time.Now()
	evaluate := func(turn int, v View) {
		for _, f := range sc.FactsAt(turn) {
			pr := ProbeRecord{Strategy: name, Seed: sc.Seed, Turn: turn, Fact: f.ID, Kind: f.Kind}
			if !v.Overflow {
				pr.Visible = containsAll(v.Text, f.Keys)
				pr.Grounded = containsAll(v.Text, f.GroundKeys())
				if !pr.Grounded {
					pr.Retrieved, pr.Cost = st.Retrieve(ctx, f.Query, f.GroundKeys())
				}
			}
			probes++
			k := sum.ByKind[f.Kind]
			k[1]++
			if pr.Grounded {
				grounded++
			}
			if pr.Grounded || pr.Retrieved {
				k[0]++
				recoverable++
			}
			sum.ByKind[f.Kind] = k
			if pr.Visible {
				visible++
			}
			if probesOut != nil {
				probesOut.Encode(pr)
			}
		}
	}
	for t, step := range sc.Steps {
		now := start.Add(time.Duration(float64(t) * c.TurnSeconds * float64(time.Second)))
		st.Tick(ctx, now)
		if step.User != nil {
			if err := st.Append(ctx, *step.User); err != nil {
				return sum, fmt.Errorf("%s turn %d user: %w", name, t, err)
			}
		}
		v, err := st.Assemble(ctx, now)
		if err != nil {
			return sum, fmt.Errorf("%s turn %d assemble: %w", name, t, err)
		}
		tr := TurnRecord{Strategy: name, Seed: sc.Seed, Turn: t, Tokens: v.Tokens, Overflow: v.Overflow, FgMS: v.ForegroundMS, EngineMS: v.EngineMS, ToolTokens: v.ToolTokens}
		if v.Report != nil {
			tr.State = v.Report.State
		}
		if v.Overflow {
			sum.Overflows++
			if sum.FirstOverflow < 0 {
				sum.FirstOverflow = t
			}
		} else {
			cr := sim.Request(now, v.Blocks)
			tr.CacheRead, tr.CacheWrite, tr.Uncached = cr.Read, cr.Write, cr.Uncached
			tr.CostUSD = c.Prices.Cost(cr, 0)
			for _, eb := range v.ExtraBlocks {
				ex := sim.Request(now, eb)
				tr.CostUSD += c.Prices.Cost(ex, 0)
				tr.CacheRead += ex.Read
				tr.CacheWrite += ex.Write
				tr.Uncached += ex.Uncached
				reads += int64(ex.Read)
				tRead += int64(ex.Read)
				tWrite += int64(ex.Write)
				tUnc += int64(ex.Uncached)
				totalIn += int64(ex.Read + ex.Write + ex.Uncached)
				sum.ExtraInput += int64(ex.Read + ex.Write + ex.Uncached)
			}
			sum.ExtraCalls += v.ExtraCalls
			sum.ExtraOutput += int64(v.ExtraOutput)
			tRead += int64(cr.Read)
			tWrite += int64(cr.Write)
			tUnc += int64(cr.Uncached)
			reads += int64(cr.Read)
			totalIn += int64(cr.Read + cr.Write + cr.Uncached)
			sum.CostUSD += tr.CostUSD
			sum.MaxContext = max(sum.MaxContext, v.Tokens)
			ctxSum += float64(v.Tokens)
			sum.ToolTokens += int64(v.ToolTokens)
		}
		sum.FgWaitMS += v.ForegroundMS
		engMS = append(engMS, v.EngineMS)
		for _, f := range sc.FactsAt(t) {
			if !f.Standing {
				continue
			}
			tr.Standing++
			standing++
			if !v.Overflow && containsAll(v.Text, f.Keys) {
				tr.StandingOK++
				standingOK++
			}
		}
		if t > 0 && sc.Steps[t-1].Probe {
			evaluate(t-1, v)
		}
		if turnsOut != nil {
			turnsOut.Encode(tr)
		}
		if err := st.Append(ctx, step.Assistant); err != nil {
			return sum, fmt.Errorf("%s turn %d assistant: %w", name, t, err)
		}
		for _, r := range step.Results {
			if err := st.Append(ctx, r); err != nil {
				return sum, fmt.Errorf("%s turn %d result: %w", name, t, err)
			}
		}
	}
	last := len(sc.Steps) - 1
	now := start.Add(time.Duration(float64(len(sc.Steps)) * c.TurnSeconds * float64(time.Second)))
	st.Tick(ctx, now)
	if v, err := st.Assemble(ctx, now); err == nil {
		evaluate(last, v)
	}
	sum.InputTokens = totalIn
	if totalIn > 0 {
		sum.CacheHitRate = float64(reads) / float64(totalIn)
	}
	sum.CacheReadTokens, sum.CacheWriteTokens, sum.UncachedTokens = tRead, tWrite, tUnc
	if tRead+tWrite+tUnc != totalIn {
		return sum, fmt.Errorf("%s: token accounting does not reconcile: %d read + %d write + %d uncached != %d input", name, tRead, tWrite, tUnc, totalIn)
	}
	if want := c.Prices.Cost(CacheResult{Read: int(tRead), Write: int(tWrite), Uncached: int(tUnc)}, 0); math.Abs(want-sum.CostUSD) > 1e-6*math.Max(1, want) {
		return sum, fmt.Errorf("%s: cost does not reconcile: %.6f from components, %.6f summed per request", name, want, sum.CostUSD)
	}
	sum.CompletionRate = float64(sum.Turns-sum.Overflows) / float64(sum.Turns)
	if probes > 0 {
		sum.ProbeGrounded = float64(grounded) / float64(probes)
	}
	served := sum.Turns - sum.Overflows
	if served > 0 {
		sum.MeanContext = ctxSum / float64(served)
	}
	sum.EngineP50, sum.EngineP95 = percentile(engMS, 0.5), percentile(engMS, 0.95)
	if probes > 0 {
		sum.ProbeVisible = float64(visible) / float64(probes)
		sum.ProbeRecoverable = float64(recoverable) / float64(probes)
	}
	sum.Probes = probes
	if standing > 0 {
		sum.StandingRetention = float64(standingOK) / float64(standing)
	}
	sum.Extra = st.Summary()
	sum.OutputCostUSD = float64(sum.ExtraOutput) * c.Prices.Output / 1e6
	sp := SummarizerPrices()
	sum.SummarizerCostUSD = (sum.Extra["summarizer_input_tokens"]*sp.Input + sum.Extra["summarizer_output_tokens"]*sp.Output) / 1e6
	sum.TotalCostUSD = sum.CostUSD + sum.OutputCostUSD + sum.SummarizerCostUSD
	sum.ExactRecovery = -1
	if es, ok := st.(*EngineStrategy); ok {
		rep, err := es.e.Verify(ctx, "bench")
		if err == nil && rep.Events > 0 {
			sum.ExactRecovery = float64(rep.EventsOK) / float64(rep.Events)
			if len(rep.Problems) > 0 {
				sum.Extra["verify_problems"] = float64(len(rep.Problems))
			}
		}
	}
	sum.WallSeconds = time.Since(wall).Seconds()
	return sum, nil
}

func (c *Config) Run(ctx context.Context) ([]RunSummary, error) {
	if err := os.MkdirAll(c.OutDir, 0o755); err != nil {
		return nil, err
	}
	tf, err := os.Create(filepath.Join(c.OutDir, "turns.jsonl"))
	if err != nil {
		return nil, err
	}
	defer tf.Close()
	pf, err := os.Create(filepath.Join(c.OutDir, "probes.jsonl"))
	if err != nil {
		return nil, err
	}
	defer pf.Close()
	sf, err := os.Create(filepath.Join(c.OutDir, "runs.jsonl"))
	if err != nil {
		return nil, err
	}
	defer sf.Close()
	te, pe, se := json.NewEncoder(tf), json.NewEncoder(pf), json.NewEncoder(sf)
	var out []RunSummary
	for _, seed := range c.Seeds {
		sc := Generate(seed, c.Turns)
		for _, name := range c.Strategies {
			s, err := c.RunOne(ctx, name, sc, te, pe)
			if err != nil {
				return out, err
			}
			se.Encode(s)
			out = append(out, s)
			if c.Log != nil {
				c.Log("seed %d %-22s ctx max %6d mean %8.0f in %9d cache %.3f cost $%.3f overflow %3d fg %8.0fms vis %.2f rec %.2f stand %.2f p95 %.2fms", seed, name, s.MaxContext, s.MeanContext, s.InputTokens, s.CacheHitRate, s.CostUSD, s.Overflows, s.FgWaitMS, s.ProbeVisible, s.ProbeRecoverable, s.StandingRetention, s.EngineP95)
			}
		}
	}
	return out, nil
}

type agg struct {
	n    int
	vals map[string][]float64
}

func meanStd(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	m := s / float64(len(xs))
	var v float64
	for _, x := range xs {
		v += (x - m) * (x - m)
	}
	if len(xs) > 1 {
		v /= float64(len(xs) - 1)
	}
	return m, math.Sqrt(v)
}

func Report(cfg Config, runs []RunSummary, title string) string {
	order := []string{}
	by := map[string]*agg{}
	for _, r := range runs {
		a, ok := by[r.Strategy]
		if !ok {
			a = &agg{vals: map[string][]float64{}}
			by[r.Strategy] = a
			order = append(order, r.Strategy)
		}
		a.n++
		add := func(k string, v float64) { a.vals[k] = append(a.vals[k], v) }
		add("in", float64(r.InputTokens)/1e6)
		add("cost", r.CostUSD)
		add("hit", r.CacheHitRate*100)
		add("max", float64(r.MaxContext)/1000)
		add("mean", r.MeanContext/1000)
		add("ovf", float64(r.Overflows))
		add("fg", r.FgWaitMS/1000)
		add("vis", r.ProbeVisible*100)
		add("rec", r.ProbeRecoverable*100)
		add("stand", r.StandingRetention*100)
		add("p95", r.EngineP95)
		add("tool", float64(r.ToolTokens)/1e6)
		add("sumtok", (r.Extra["summarizer_input_tokens"]+r.Extra["summarizer_output_tokens"])/1e6)
		add("exact", r.ExactRecovery*100)
		add("extra", float64(r.ExtraCalls))
		add("total", r.TotalCostUSD)
		add("compl", r.CompletionRate*100)
		add("ground", r.ProbeGrounded*100)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", title)
	fmt.Fprintf(&b, "Window %d tokens (effective input capacity %d), %d turns per session, %d seeds, summarizer %s, %.0f s simulated per turn.\n\n", cfg.Window, basePolicy(cfg.Window, cfg.MaxOutput).EffectiveCapacity(), cfg.Turns, len(cfg.Seeds), cfg.SumName, cfg.TurnSeconds)
	b.WriteString("| strategy | completion % | input Mtok | input cost $ | total cost $ | cache hit % | max ctx k | mean ctx k | overflow turns | fg wait s | probes visible % | probes grounded % | probes recoverable % | standing rules % | tool Mtok sent | summarizer Mtok | extra primary calls | engine p95 ms | exact recovery % |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	f := func(a *agg, k string, prec int) string {
		m, s := meanStd(a.vals[k])
		if a.n > 1 && s > 0 {
			return fmt.Sprintf("%.*f ± %.*f", prec, m, prec, s)
		}
		return fmt.Sprintf("%.*f", prec, m)
	}
	for _, name := range order {
		a := by[name]
		exact := f(a, "exact", 1)
		if m, _ := meanStd(a.vals["exact"]); m < 0 {
			exact = "n/a"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", name, f(a, "compl", 1), f(a, "in", 2), f(a, "cost", 2), f(a, "total", 2), f(a, "hit", 1), f(a, "max", 1), f(a, "mean", 1), f(a, "ovf", 0), f(a, "fg", 1), f(a, "vis", 1), f(a, "ground", 1), f(a, "rec", 1), f(a, "stand", 1), f(a, "tool", 2), f(a, "sumtok", 2), f(a, "extra", 0), f(a, "p95", 2), exact)
	}
	b.WriteString("\nProbe recovery by fact kind (recoverable / asked, summed over seeds):\n\n| strategy | ")
	kinds := []string{"constraint", "decision", "rejected", "value", "symbol", "error"}
	b.WriteString(strings.Join(kinds, " | "))
	b.WriteString(" |\n|---|" + strings.Repeat("---|", len(kinds)) + "\n")
	for _, name := range order {
		tot := map[string][2]int{}
		for _, r := range runs {
			if r.Strategy != name {
				continue
			}
			for k, v := range r.ByKind {
				t := tot[k]
				t[0] += v[0]
				t[1] += v[1]
				tot[k] = t
			}
		}
		fmt.Fprintf(&b, "| %s |", name)
		for _, k := range kinds {
			fmt.Fprintf(&b, " %d/%d |", tot[k][0], tot[k][1])
		}
		b.WriteString("\n")
	}
	return b.String()
}
