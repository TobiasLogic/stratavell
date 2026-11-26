package engine

import (
	"context"
	"time"

	"github.com/TobiasLogic/stratavell/msg"
)

type predictor struct {
	growth   float64
	gap      float64
	lat      float64
	latPeak  float64
	lastTurn time.Time
	appended int
	samples  int
	lastHead int64
}

func (p *predictor) observeAppend(n int) { p.appended += n }

func (p *predictor) observeTurn(now time.Time, grew int) {
	if !p.lastTurn.IsZero() {
		g := now.Sub(p.lastTurn).Seconds()
		if p.gap == 0 {
			p.gap = g
		} else {
			p.gap = 0.8*p.gap + 0.2*g
		}
	}
	p.lastTurn = now
	if p.samples == 0 {
		p.growth = float64(grew)
	} else {
		p.growth = 0.7*p.growth + 0.3*float64(grew)
	}
	p.samples++
	p.appended = 0
}

func (p *predictor) observeLatency(s float64) {
	if p.lat == 0 {
		p.lat = s
	} else {
		p.lat = 0.7*p.lat + 0.3*s
	}
	p.latPeak = max(s, p.latPeak*0.9)
}

func (p *predictor) turnsNeeded(safety float64) float64 {
	lat := max(p.lat, p.latPeak)
	if lat == 0 {
		lat = 20
	}
	gap := p.gap
	if gap <= 0 {
		gap = 5
	}
	return safety * lat / gap
}

func (a *Agent) capacity() int { return a.pol.EffectiveCapacity() }

func (a *Agent) estimateTokens(it *item) int {
	if it.meta.Oversize {
		return 220 + a.e.count(it.meta.Preview)
	}
	return it.ev.Tokens
}

func (a *Agent) safeCuts() []bool {
	safe := make([]bool, len(a.raw))
	open := map[string]bool{}
	for i, it := range a.raw {
		switch it.ev.Role {
		case msg.RoleAssistant:
			for _, id := range it.callIDs {
				open[id] = true
			}
		case msg.RoleTool:
			delete(open, it.ev.ToolCallID)
		}
		safe[i] = len(open) == 0
		if safe[i] && i+1 < len(a.raw) && a.raw[i+1].ev.Role == msg.RoleTool {
			safe[i] = false
		}
	}
	return safe
}

func (a *Agent) recentStart(boundary int64) int64 {
	if len(a.raw) == 0 {
		return a.head + 1
	}
	budget := int(a.pol.Budget.RecentFraction * float64(a.capacity()))
	safe := a.safeCuts()
	acc, turns := 0, 0
	idx := len(a.raw)
	for i := len(a.raw) - 1; i >= 0; i-- {
		it := a.raw[i]
		if it.ev.Seq <= boundary {
			break
		}
		acc += a.estimateTokens(it)
		if it.ev.Role == msg.RoleAssistant {
			turns++
		}
		idx = i
		if acc >= budget && turns >= a.pol.Budget.MinRecentTurns {
			break
		}
	}
	for idx > 0 && !safe[idx-1] {
		idx--
	}
	if idx >= len(a.raw) {
		return a.head + 1
	}
	return a.raw[idx].ev.Seq
}

func (a *Agent) coveredHi() int64 {
	hi := a.boundary
	for _, r := range a.ready {
		if r.job.Kind == jobLeaf && r.job.Hi > hi {
			hi = r.job.Hi
		}
	}
	for _, j := range a.leafJobs {
		if j.Hi > hi {
			hi = j.Hi
		}
	}
	return hi
}

type chunk struct {
	lo, hi int
	tok    int
}

func (a *Agent) chunksBetween(fromSeq, toSeq int64, target, minTok int, exact [][2]int64) []chunk {
	safe := a.safeCuts()
	var out []chunk
	start := -1
	acc := 0
	for i, it := range a.raw {
		if it.ev.Seq < fromSeq || it.ev.Seq > toSeq {
			continue
		}
		if start < 0 {
			start = i
			acc = 0
		}
		acc += a.e.count(a.summarizerText(it)) + 8
		cutHere := safe[i] && acc >= target
		for _, ex := range exact {
			if it.ev.Seq == ex[1] && a.raw[start].ev.Seq == ex[0] {
				cutHere = true
			}
		}
		if cutHere {
			out = append(out, chunk{start, i, acc})
			start = -1
		}
	}
	if start >= 0 && acc >= minTok {
		end := -1
		for i := len(a.raw) - 1; i >= start; i-- {
			if a.raw[i].ev.Seq <= toSeq && safe[i] {
				end = i
				break
			}
		}
		if end >= start {
			tok := 0
			for i := start; i <= end; i++ {
				tok += a.e.count(a.summarizerText(a.raw[i])) + 8
			}
			if tok >= minTok {
				out = append(out, chunk{start, end, tok})
			}
		}
	}
	return out
}

func (a *Agent) leafSpec(c chunk) *jobSpec {
	j := &jobSpec{
		ID:      a.e.ids.New("jb_"),
		Session: a.s.id,
		Agent:   a.id,
		Kind:    jobLeaf,
		Level:   1,
		BaseGen: a.gen,
		Lo:      a.raw[c.lo].ev.Seq,
		Hi:      a.raw[c.hi].ev.Seq,
		SrcTok:  c.tok,
		Created: a.e.clock.Now(),
	}
	items := make([]SummaryInputItem, 0, c.hi-c.lo+1)
	for i := c.lo; i <= c.hi; i++ {
		it := a.raw[i]
		j.Sources = append(j.Sources, it.ev.ID)
		items = append(items, SummaryInputItem{ID: it.ev.ID, Seq: it.ev.Seq, Role: string(it.ev.Role), Tool: it.ev.ToolName, Text: a.summarizerText(it)})
	}
	prior := ""
	if n := len(a.frontier); n > 0 {
		prior = a.frontier[n-1].Text
	}
	j.Input = SummarizeInput{SessionID: a.s.id, AgentID: a.id, Level: 1, Items: items, Prior: prior, MaxTokens: a.summaryBudget(c.tok), PromptVersion: a.pol.Compaction.PromptVersion}
	return j
}

func (a *Agent) summaryBudget(src int) int {
	t := int(a.pol.Compaction.SummaryRatio * float64(src))
	t = max(t, 200)
	return min(t, a.pol.Compaction.SummaryMaxTokens)
}

func (a *Agent) condenseSpec(nodes []*SummaryNode) *jobSpec {
	lvl := 0
	src := 0
	for _, n := range nodes {
		lvl = max(lvl, n.Level)
		src += n.Tokens
	}
	j := &jobSpec{
		ID:      a.e.ids.New("jb_"),
		Session: a.s.id,
		Agent:   a.id,
		Kind:    jobCondense,
		Level:   lvl + 1,
		BaseGen: a.gen,
		Lo:      nodes[0].Lo,
		Hi:      nodes[len(nodes)-1].Hi,
		SrcTok:  src,
		Created: a.e.clock.Now(),
	}
	var items []SummaryInputItem
	for _, n := range nodes {
		j.Sources = append(j.Sources, n.ID)
		var refs []string
		for _, f := range n.Facts {
			refs = append(refs, f.Refs...)
		}
		items = append(items, SummaryInputItem{ID: n.ID, Role: "summary", Text: n.Text + factsText(n.Facts), Lo: n.Lo, Hi: n.Hi, Refs: refs})
	}
	j.Input = SummarizeInput{SessionID: a.s.id, AgentID: a.id, Level: lvl + 1, Items: items, MaxTokens: min(a.pol.Compaction.SummaryMaxTokens, max(300, src/3)), PromptVersion: a.pol.Compaction.PromptVersion}
	return j
}

func factsText(fs []Fact) string {
	if len(fs) == 0 {
		return ""
	}
	s := "\nfacts:"
	for _, f := range fs {
		s += "\n- [" + f.Kind + "] " + f.Text
		for i, r := range f.Refs {
			if i == 0 {
				s += " ("
			} else {
				s += ", "
			}
			s += r
			if i == len(f.Refs)-1 {
				s += ")"
			}
		}
	}
	return s
}

func (a *Agent) estimateUsage() int {
	return a.lastEst + a.pred.appended
}

func (a *Agent) shouldCompact(u int) bool {
	c := a.capacity()
	return a.e.trigger.ShouldCompact(Pressure{
		Tokens:        u,
		Capacity:      c,
		Usage:         float64(u) / float64(c),
		GrowthPerTurn: a.pred.growth,
		TurnsNeeded:   a.pred.turnsNeeded(a.pol.Compaction.PredictSafety),
		Samples:       a.pred.samples,
		Inflight:      len(a.leafJobs),
		Thresholds:    a.pol.Thresholds,
	})
}

func (a *Agent) maybeSchedule(ctx context.Context, why string) {
	if !a.pol.Compaction.Enabled || !a.pol.Compaction.Async || a.e.getSummarizer() == nil {
		return
	}
	u := a.estimateUsage()
	if !a.shouldCompact(u) {
		return
	}
	a.scheduleLeaves(ctx, u)
	a.scheduleCondense(ctx)
}

func (a *Agent) scheduleLeaves(ctx context.Context, u int) []*jobSpec {
	free := a.pol.Compaction.MaxInflightAgent - len(a.leafJobs)
	if free <= 0 {
		return nil
	}
	c := a.capacity()
	need := u - int(a.pol.Thresholds.Target*float64(c))
	if need <= 0 {
		need = a.pol.Compaction.LeafChunkTokens
	}
	from := a.coveredHi() + 1
	to := a.recentStart(a.boundary) - 1
	if to < from {
		return nil
	}
	chunks := a.chunksBetween(from, to, a.pol.Compaction.LeafChunkTokens, a.pol.Compaction.MinChunkTokens, nil)
	var out []*jobSpec
	freed := 0
	for _, ch := range chunks {
		if free <= 0 || freed >= need {
			break
		}
		j := a.leafSpec(ch)
		if err := a.e.persistJob(ctx, j); err != nil {
			a.e.metrics.inc("schedule_errors", 1)
			break
		}
		if err := a.e.sched.enqueue(j); err != nil {
			_ = a.e.markJob(ctx, j.ID, "rejected", err.Error())
			break
		}
		a.leafJobs[j.ID] = *j
		out = append(out, j)
		free--
		freed += int(float64(ch.tok) * (1 - a.pol.Compaction.SummaryRatio))
		a.e.metrics.inc("jobs_scheduled", 1)
	}
	return out
}

func (a *Agent) summaryTokens() int {
	n := 0
	for _, s := range a.frontier {
		n += s.Tokens
	}
	return n
}

func (a *Agent) condenseCandidate() []*SummaryNode {
	if len(a.frontier) < 2 {
		return nil
	}
	over := float64(a.summaryTokens()) > a.pol.Budget.SummaryFraction*float64(a.capacity())
	if !over && len(a.frontier) < a.pol.Compaction.CondenseMaxNodes {
		return nil
	}
	fan := a.pol.Compaction.CondenseFanin
	lvl := a.frontier[0].Level
	for _, n := range a.frontier {
		lvl = min(lvl, n.Level)
	}
	start := -1
	for i, n := range a.frontier {
		if n.Level == lvl {
			start = i
			break
		}
	}
	end := start
	for end+1 < len(a.frontier) && a.frontier[end+1].Level == lvl && end-start+1 < fan {
		end++
	}
	if end-start+1 < 2 {
		if len(a.frontier) >= 2 {
			return a.frontier[:min(fan, len(a.frontier)-1)]
		}
		return nil
	}
	if end == len(a.frontier)-1 && end-start+1 > 2 {
		end--
	}
	return a.frontier[start : end+1]
}

func (a *Agent) scheduleCondense(ctx context.Context) {
	if a.condenseJob != "" {
		return
	}
	nodes := a.condenseCandidate()
	if len(nodes) < 2 {
		return
	}
	j := a.condenseSpec(nodes)
	if err := a.e.persistJob(ctx, j); err != nil {
		return
	}
	if err := a.e.sched.enqueue(j); err != nil {
		_ = a.e.markJob(ctx, j.ID, "rejected", err.Error())
		return
	}
	a.condenseJob = j.ID
	a.e.metrics.inc("jobs_scheduled", 1)
}
