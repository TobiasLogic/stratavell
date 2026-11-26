package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/TobiasLogic/stratavell/adapters/anthropic"
	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/internal/tokens"
	"github.com/TobiasLogic/stratavell/msg"
)

type View struct {
	Text         string
	Blocks       []CacheBlock
	Tokens       int
	Overflow     bool
	ForegroundMS float64
	EngineMS     float64
	ExtraCalls   int
	ExtraInput   int
	ExtraOutput  int
	ToolTokens   int
	Report       *engine.Report
	Context      *engine.Context
	ExtraBlocks  [][]CacheBlock
}

type Strategy interface {
	Name() string
	Append(ctx context.Context, m msg.Message) error
	Assemble(ctx context.Context, now time.Time) (View, error)
	Tick(ctx context.Context, now time.Time)
	Retrieve(ctx context.Context, query string, keys []string) (bool, int)
	Summary() map[string]float64
	Close() error
}

type LatencyModel struct {
	Base      time.Duration
	PrefillPS float64
	DecodePS  float64
}

func DefaultLatency() LatencyModel {
	return LatencyModel{Base: 800 * time.Millisecond, PrefillPS: 4000, DecodePS: 60}
}

func (l LatencyModel) Of(in, out int) time.Duration {
	return l.Base + time.Duration(float64(in)/l.PrefillPS*float64(time.Second)) + time.Duration(float64(out)/l.DecodePS*float64(time.Second))
}

type meteredSummarizer struct {
	inner  engine.Summarizer
	lat    LatencyModel
	mu     sync.Mutex
	fg     bool
	fgMS   float64
	bgMS   float64
	calls  int
	inTok  int
	outTok int
	real   bool
}

func (m *meteredSummarizer) Summarize(ctx context.Context, in engine.SummarizeInput) (engine.SummarizeOutput, error) {
	start := time.Now()
	out, err := m.inner.Summarize(ctx, in)
	el := time.Since(start)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	itok := out.InputTokens
	if itok == 0 {
		for _, it := range in.Items {
			itok += tokens.Estimate(it.Text)
		}
	}
	m.inTok += itok
	m.outTok += out.OutputTokens
	ms := float64(m.lat.Of(itok, out.OutputTokens).Microseconds()) / 1000
	if m.real {
		ms = float64(el.Microseconds()) / 1000
	}
	if m.fg {
		m.fgMS += ms
	} else {
		m.bgMS += ms
	}
	return out, err
}

func (m *meteredSummarizer) setFG(v bool) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fg = v
	x := m.fgMS
	if !v {
		m.fgMS = 0
	}
	return x
}

type EngineStrategy struct {
	name   string
	e      *engine.Engine
	a      *engine.Agent
	clock  *engine.ManualClock
	summ   *meteredSummarizer
	lat    LatencyModel
	system string
	tools  []msg.ToolDef
	memory bool
	prevBP string
	waitMS float64
	stats  map[string]float64
}

type EngineConfig struct {
	Name       string
	Policy     engine.Policy
	Summarizer engine.Summarizer
	RealTiming bool
	Dir        string
	Seed       uint64
	Latency    LatencyModel
}

var hostTools = []msg.ToolDef{
	{Name: "read_file", Description: "Read a file from the repository.", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
	{Name: "write_file", Description: "Write a file.", Schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)},
	{Name: "bash", Description: "Run a shell command in the repository.", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`)},
}

const hostSystem = "You are a careful software engineering agent working in a Go repository. Use the tools to read, edit and test code. Follow the user's instructions exactly."

func NewEngineStrategy(ctx context.Context, cfg EngineConfig, start time.Time) (*EngineStrategy, error) {
	clock := engine.NewManualClock(start)
	lat := cfg.Latency
	if lat.PrefillPS == 0 {
		lat = DefaultLatency()
	}
	ms := &meteredSummarizer{inner: cfg.Summarizer, lat: lat, real: cfg.RealTiming}
	e, err := engine.Open(ctx, engine.Options{Path: filepath.Join(cfg.Dir, cfg.Name+".db"), Policy: cfg.Policy, Summarizer: ms, Clock: clock, IDs: engine.NewSeededIDs(clock, cfg.Seed), Manual: true, SyncIndex: true})
	if err != nil {
		return nil, err
	}
	s, err := e.OpenSession(ctx, "bench", engine.SessionConfig{})
	if err != nil {
		return nil, err
	}
	a, err := s.Agent(ctx, "main")
	if err != nil {
		return nil, err
	}
	return &EngineStrategy{name: cfg.Name, e: e, a: a, clock: clock, summ: ms, lat: lat, system: hostSystem, tools: hostTools, memory: cfg.Policy.Retrieval.Enabled, stats: map[string]float64{}}, nil
}

func (s *EngineStrategy) Name() string           { return s.name }
func (s *EngineStrategy) Engine() *engine.Engine { return s.e }
func (s *EngineStrategy) Agent() *engine.Agent   { return s.a }

func (s *EngineStrategy) Append(ctx context.Context, m msg.Message) error {
	_, err := s.a.Append(ctx, m)
	return err
}

func (s *EngineStrategy) advance(now time.Time) {
	if d := now.Sub(s.clock.Now()); d > 0 {
		s.clock.Advance(d)
	}
}

func (s *EngineStrategy) Tick(ctx context.Context, now time.Time) {
	s.advance(now)
	for _, j := range s.e.PendingJobs("bench", "main") {
		out := min(s.a.Policy().Compaction.SummaryMaxTokens, max(200, int(s.a.Policy().Compaction.SummaryRatio*float64(j.SrcTok))))
		if !j.Created.Add(s.lat.Of(j.SrcTok, out)).After(now) {
			s.e.RunJob(ctx, j.ID)
		}
	}
}

func (s *EngineStrategy) Assemble(ctx context.Context, now time.Time) (View, error) {
	s.advance(now)
	s.summ.setFG(true)
	t0 := time.Now()
	cx, err := s.a.Assemble(ctx, engine.AssembleRequest{System: s.system, Tools: s.tools, IncludeMemoryTools: s.memory})
	el := float64(time.Since(t0).Microseconds()) / 1000
	fg := s.summ.setFG(false)
	if err != nil {
		if errors.Is(err, engine.ErrContextOverflow) || errors.Is(err, engine.ErrProtectedOverflow) {
			s.stats["overflows"]++
			return View{Overflow: true, ForegroundMS: fg, EngineMS: el - 0}, nil
		}
		return View{}, err
	}
	v, err := viewOf(cx)
	if err != nil {
		return View{}, err
	}
	v.ForegroundMS = fg
	v.EngineMS = el
	v.Report = &cx.Report
	v.Context = cx
	return v, nil
}

func viewOf(cx *engine.Context) (View, error) {
	req, err := anthropic.Render(cx, anthropic.Options{Model: "bench", MaxTokens: 8000})
	if err != nil {
		return View{}, err
	}
	var v View
	var b strings.Builder
	for _, t := range req.Tools {
		k, _ := json.Marshal(Tool(t))
		v.Blocks = append(v.Blocks, CacheBlock{Key: "tool|" + string(k), Tokens: tokens.Estimate(string(k)), Break: t.CacheControl != nil})
	}
	for _, sb := range req.System {
		v.Blocks = append(v.Blocks, CacheBlock{Key: "system|" + sb.Text, Tokens: tokens.Estimate(sb.Text), Break: sb.CacheControl != nil})
		b.WriteString(sb.Text)
		b.WriteByte('\n')
	}
	for mi, m := range req.Messages {
		for bi, blk := range m.Content {
			brk := blk.CacheControl != nil
			blk.CacheControl = nil
			k, _ := json.Marshal(blk)
			key := string(k)
			if bi == 0 {
				key = fmt.Sprintf("%s#%d|%s", m.Role, mi, key)
			}
			grp := ""
			if blk.Type == "tool_result" {
				grp = fmt.Sprintf("tr%d", mi)
			}
			if blk.Type == "tool_use" {
				grp = fmt.Sprintf("tu%d", mi)
			}
			text := blk.Text + blk.Thinking + string(blk.Input)
			if s, ok := blk.Content.(string); ok {
				text += s
				v.ToolTokens += tokens.Estimate(s)
			}
			v.Blocks = append(v.Blocks, CacheBlock{Key: key, Tokens: tokens.Estimate(text) + 3, Break: brk, Group: grp})
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	v.Text = b.String()
	v.Tokens = cx.Tokens
	return v, nil
}

type Tool anthropic.Tool

var reIDs = regexp.MustCompile(`\b(?:ev|sm)_[0-9A-Z]{26}\b`)

func (s *EngineStrategy) Retrieve(ctx context.Context, query string, keys []string) (bool, int) {
	if !s.memory {
		return false, 0
	}
	hits, err := s.a.Search(ctx, query, engine.SearchFilters{}, 5)
	if err != nil {
		return false, 0
	}
	cost := 0
	for _, h := range hits {
		cost += tokens.Estimate(h.Snippet) + 20
		if containsAll(h.Snippet, keys) {
			return true, cost
		}
	}
	for i, h := range hits {
		if i >= 3 {
			break
		}
		var text string
		if h.Kind == "summary" {
			out, err := s.a.Expand(ctx, h.ID, 0, 0, 4000)
			if err != nil {
				continue
			}
			text = out
		} else {
			_, m, err := s.a.GetEvent(ctx, h.ID)
			if err != nil {
				continue
			}
			text = m.PlainText()
		}
		if t := tokens.Estimate(text); t > 6000 {
			cost += 6000
		} else {
			cost += t
		}
		if containsAll(text, keys) {
			return true, cost
		}
	}
	return false, cost
}

func (s *EngineStrategy) RetrieveText(ctx context.Context, query string, budget int) string {
	if !s.memory {
		return ""
	}
	hits, err := s.a.Search(ctx, query, engine.SearchFilters{}, 5)
	if err != nil || len(hits) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[memory_search %q]\n", query)
	for _, h := range hits {
		where := ""
		if len(h.Paths) > 0 {
			where = ", path " + strings.Join(h.Paths, ", ")
		}
		fmt.Fprintf(&b, "- %s (%s%s): %s\n", h.ID, h.Provenance, where, h.Snippet)
	}
	for i, h := range hits {
		if i >= 2 {
			break
		}
		var text string
		if h.Kind == "summary" {
			text, _ = s.a.Expand(ctx, h.ID, 0, 0, budget/2)
		} else if t, err := s.a.ReadEvent(ctx, h.ID, 0); err == nil {
			text = t
		}
		if tokens.Estimate(text) > budget/2 {
			text = text[:min(len(text), budget*2)]
		}
		fmt.Fprintf(&b, "%s\n", text)
	}
	return b.String()
}

func containsAll(text string, keys []string) bool {
	lt := strings.ToLower(text)
	for _, k := range keys {
		if !strings.Contains(lt, strings.ToLower(k)) {
			return false
		}
	}
	return true
}

func (s *EngineStrategy) Summary() map[string]float64 {
	snap := s.e.Metrics().Snapshot()
	out := map[string]float64{}
	for k, v := range s.stats {
		out[k] = v
	}
	for _, k := range []string{"summaries_published", "pointer_nodes", "emergencies", "mask_epochs", "generations", "job_failures", "fallbacks", "jobs_scheduled", "summaries_stale"} {
		out[k] = snap.Counters[k]
	}
	s.summ.mu.Lock()
	out["summarizer_calls"] = float64(s.summ.calls)
	out["summarizer_input_tokens"] = float64(s.summ.inTok)
	out["summarizer_output_tokens"] = float64(s.summ.outTok)
	out["background_summarizer_ms"] = s.summ.bgMS
	s.summ.mu.Unlock()
	if h, ok := snap.Histograms["append_ms"]; ok {
		out["append_p95_ms"] = h.P95
		out["append_p50_ms"] = h.P50
	}
	if h, ok := snap.Histograms["assemble_ms"]; ok {
		out["assemble_p95_ms"] = h.P95
		out["assemble_p50_ms"] = h.P50
	}
	if h, ok := snap.Histograms["retrieval_ms"]; ok {
		out["retrieval_p95_ms"] = h.P95
		out["retrieval_p50_ms"] = h.P50
	}
	st, _ := s.e.Stats(context.Background())
	out["db_bytes"] = float64(st.DBBytes + st.WALBytes)
	out["raw_bytes"] = float64(st.EventRaw)
	return out
}

func (s *EngineStrategy) Close() error { return s.e.Close() }

type listStrategy struct {
	name     string
	summ     *meteredSummarizer
	capacity int
	fixed    int
	msgs     []msg.Message
	ids      []string
	summary  string
	stats    map[string]float64
	noa      *noaState
	n        int
	memTools bool
}

func (l *listStrategy) Name() string { return l.name }

func (l *listStrategy) Append(ctx context.Context, m msg.Message) error {
	l.n++
	l.msgs = append(l.msgs, m)
	l.ids = append(l.ids, fmt.Sprintf("m%05d", l.n))
	if l.noa != nil {
		l.noa.raw = append(l.noa.raw, m)
	}
	return nil
}

func (l *listStrategy) Tick(ctx context.Context, now time.Time) {}

func (l *listStrategy) Retrieve(ctx context.Context, q string, keys []string) (bool, int) {
	return false, 0
}

func (l *listStrategy) Close() error { return nil }

func (l *listStrategy) Summary() map[string]float64 {
	out := map[string]float64{}
	for k, v := range l.stats {
		out[k] = v
	}
	out["summarizer_calls"] = float64(l.summ.calls)
	out["summarizer_input_tokens"] = float64(l.summ.inTok)
	out["summarizer_output_tokens"] = float64(l.summ.outTok)
	return out
}

func msgTokens(m msg.Message) int {
	n := 6
	for _, p := range m.Parts {
		n += tokens.Estimate(p.Text) + tokens.Estimate(string(p.Input)) + 8
	}
	return n
}

func renderForSummary(ms []msg.Message, maxObs int) []engine.SummaryInputItem {
	var items []engine.SummaryInputItem
	for i, m := range ms {
		text := m.PlainText()
		if m.Role == msg.RoleTool && tokens.Estimate(text) > maxObs {
			lines := strings.Split(text, "\n")
			if len(lines) > 30 {
				text = strings.Join(lines[:15], "\n") + "\n...\n" + strings.Join(lines[len(lines)-10:], "\n")
			}
		}
		tool := ""
		if p, ok := m.ToolResultPart(); ok {
			tool = p.ToolName
		}
		items = append(items, engine.SummaryInputItem{ID: fmt.Sprintf("m%d", i), Seq: int64(i + 1), Role: string(m.Role), Tool: tool, Text: text})
	}
	return items
}

func safeStart(ms []msg.Message, from int) int {
	for from < len(ms) && ms[from].Role == msg.RoleTool {
		from++
	}
	return from
}

func (l *listStrategy) contextOf(prefix string, msgs []msg.Message) *engine.Context {
	tools := hostTools
	if l.memTools {
		tools = append(append([]msg.ToolDef(nil), hostTools...), engine.MemoryTools()...)
	}
	cx := &engine.Context{System: []engine.SystemBlock{{Text: hostSystem}}, Tools: tools}
	if prefix != "" {
		cx.Messages = append(cx.Messages, engine.AMessage{Role: msg.RoleUser, Kind: "archive", Parts: []msg.Part{{Type: msg.PartText, Text: prefix}}})
	}
	for _, m := range msgs {
		cx.Messages = append(cx.Messages, engine.AMessage{Role: m.Role, Parts: m.Parts, Kind: "history"})
	}
	cx.Breakpoints = []engine.Breakpoint{{Message: -1, Part: 0}}
	if n := len(cx.Messages); n > 0 {
		cx.Breakpoints = append(cx.Breakpoints, engine.Breakpoint{Message: n - 1, Part: len(cx.Messages[n-1].Parts) - 1})
		if n > 3 {
			cx.Breakpoints = append(cx.Breakpoints, engine.Breakpoint{Message: n - 4, Part: len(cx.Messages[n-4].Parts) - 1})
		}
	}
	return cx
}

func NewConventional(sum engine.Summarizer, capacity int, lat LatencyModel, real bool) Strategy {
	return &conventional{listStrategy: listStrategy{name: "B-conventional", summ: &meteredSummarizer{inner: sum, lat: lat, real: real}, capacity: capacity, fixed: 900, stats: map[string]float64{}}}
}

type conventional struct {
	listStrategy
	pins    bool
	pinned  []string
	archive *EngineStrategy
}

func (c *conventional) Append(ctx context.Context, m msg.Message) error {
	if c.archive != nil {
		if err := c.archive.Append(ctx, m); err != nil {
			return err
		}
	}
	return c.listStrategy.Append(ctx, m)
}

func (c *conventional) Retrieve(ctx context.Context, q string, keys []string) (bool, int) {
	if c.archive == nil {
		return false, 0
	}
	return c.archive.Retrieve(ctx, q, keys)
}

func (c *conventional) RetrieveText(ctx context.Context, q string, budget int) string {
	if c.archive == nil {
		return ""
	}
	return c.archive.RetrieveText(ctx, q, budget)
}

func (c *conventional) Close() error {
	if c.archive != nil {
		return c.archive.Close()
	}
	return nil
}

func (c *conventional) pinText() string {
	if len(c.pinned) == 0 {
		return ""
	}
	return "\n\nStanding instructions from the user, repeated verbatim:\n- " + strings.Join(c.pinned, "\n- ")
}

func (c *conventional) total(msgs []msg.Message) int {
	n := c.fixed + tokens.Estimate(c.summary) + tokens.Estimate(c.pinText())
	for _, m := range msgs {
		n += msgTokens(m)
	}
	return n
}

func (c *conventional) Assemble(ctx context.Context, now time.Time) (View, error) {
	fg := 0.0
	if float64(c.total(c.msgs)) >= 0.90*float64(c.capacity) && len(c.msgs) > 8 {
		keep := safeStart(c.msgs, len(c.msgs)-8)
		head := c.msgs[:keep]
		items := renderForSummary(head, 1500)
		if c.summary != "" {
			items = append([]engine.SummaryInputItem{{ID: "prev", Role: "summary", Text: c.summary, Lo: 1, Hi: 1}}, items...)
		}
		budget := c.capacity / 2
		for len(items) > 1 {
			t := 0
			for _, it := range items {
				t += tokens.Estimate(it.Text)
			}
			if t <= budget {
				break
			}
			items = items[len(items)/5+1:]
			c.stats["dropped_from_summary_input"]++
		}
		c.summ.setFG(true)
		out, err := c.summ.Summarize(ctx, engine.SummarizeInput{Level: 2, Items: items, MaxTokens: 3000})
		fg = c.summ.setFG(false)
		if err == nil {
			c.summary = out.Text + factsBlock(out.Facts)
		}
		if c.pins {
			for _, m := range head {
				if m.Role == msg.RoleUser {
					c.pinned = append(c.pinned, m.PlainText())
				}
			}
		}
		c.msgs = append([]msg.Message(nil), c.msgs[keep:]...)
		c.stats["compactions"]++
	}
	cx := c.contextOf(c.summaryPrefix(), c.msgs)
	if c.total(c.msgs) > c.capacity {
		c.stats["overflows"]++
		return View{Overflow: true, ForegroundMS: fg}, nil
	}
	v, err := viewOf(cx)
	v.ForegroundMS = fg
	v.Tokens = c.total(c.msgs)
	v.Context = cx
	return v, err
}

func (c *conventional) summaryPrefix() string {
	if c.summary == "" {
		return ""
	}
	return "This session continues from an earlier conversation that was compacted. Summary:\n" + c.summary + c.pinText()
}

func factsBlock(fs []engine.Fact) string {
	if len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	for _, f := range fs {
		fmt.Fprintf(&b, "- [%s] %s\n", f.Kind, f.Text)
	}
	return b.String()
}
