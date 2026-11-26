package bench

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/internal/tokens"
	"github.com/TobiasLogic/palimpsest/msg"
)

type noaBlock struct {
	lo, hi int
	tier   int
	text   string
	live   bool
}

type noaState struct {
	raw       []msg.Message
	blocks    []*noaBlock
	lastNudge int
	nudged    bool
}

type noa struct {
	listStrategy
	st  *noaState
	lat LatencyModel
}

func NewNoa(sum engine.Summarizer, capacity int, lat LatencyModel, real bool) Strategy {
	st := &noaState{}
	n := &noa{listStrategy: listStrategy{name: "C-noa", summ: &meteredSummarizer{inner: sum, lat: lat, real: real}, capacity: capacity, fixed: 1100, stats: map[string]float64{}, noa: st}, st: st, lat: lat}
	return n
}

func (n *noa) covering(i int) *noaBlock {
	for _, b := range n.st.blocks {
		if b.live && i >= b.lo && i <= b.hi {
			return b
		}
	}
	return nil
}

func (n *noa) view(truncate bool) ([]msg.Message, int) {
	var out []msg.Message
	total := n.fixed
	raw := n.st.raw
	for i := 0; i < len(raw); i++ {
		if b := n.covering(i); b != nil {
			if i == b.lo {
				m := msg.Text(msg.RoleUser, fmt.Sprintf("[compressed m%05d-m%05d tier %d]\n%s", b.lo+1, b.hi+1, b.tier, b.text))
				out = append(out, m)
				total += msgTokens(m)
			}
			continue
		}
		m := raw[i]
		if truncate && m.Role == msg.RoleTool && i < len(raw)-5 {
			if p, ok := m.ToolResultPart(); ok && tokens.Estimate(p.Text) > 1000 && len(p.Text) > 4000 {
				q := p
				q.Text = p.Text[:2000] + "\n[... truncated ...]\n" + p.Text[len(p.Text)-2000:]
				m = msg.Message{Role: msg.RoleTool, Parts: []msg.Part{q}}
			}
		}
		out = append(out, m)
		total += msgTokens(m)
	}
	return out, total
}

func (n *noa) protectedFrom() int {
	raw := n.st.raw
	start := len(raw) - 5
	acc := 0
	for i := len(raw) - 1; i >= 0; i-- {
		acc += msgTokens(raw[i])
		if acc >= 5000 && i < start {
			break
		}
		if i < start {
			start = i
		}
	}
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i].Role == msg.RoleUser {
			if i < start {
				start = i
			}
			break
		}
	}
	for start > 0 && start < len(raw) && raw[start].Role == msg.RoleTool {
		start--
	}
	return max(start, 1)
}

func (n *noa) compressible() (int, int, int) {
	limit := n.protectedFrom()
	lo := -1
	tok := 0
	for i := 1; i < limit; i++ {
		if n.covering(i) != nil {
			if lo >= 0 {
				break
			}
			continue
		}
		if lo < 0 {
			lo = i
		}
		tok += msgTokens(n.st.raw[i])
	}
	if lo < 0 {
		return -1, -1, 0
	}
	hi := lo
	for i := lo; i < limit && n.covering(i) == nil; i++ {
		hi = i
	}
	for hi > lo && n.st.raw[hi].Role == msg.RoleAssistant && len(n.st.raw[hi].ToolCalls()) > 0 {
		hi--
	}
	for hi+1 < len(n.st.raw) && n.st.raw[hi+1].Role == msg.RoleTool {
		hi++
	}
	return lo, hi, tok
}

func (n *noa) summarize(ctx context.Context, items []engine.SummaryInputItem, budget int) (string, float64) {
	n.summ.setFG(true)
	out, err := n.summ.Summarize(ctx, engine.SummarizeInput{Level: 1, Items: items, MaxTokens: budget})
	ms := n.summ.setFG(false)
	if err != nil {
		return "", ms
	}
	return out.Text + factsBlock(out.Facts), ms
}

func (n *noa) Assemble(ctx context.Context, now time.Time) (View, error) {
	fg := 0.0
	extraCalls, extraIn, extraOut := 0, 0, 0
	L := float64(n.capacity)
	_, T := n.view(false)
	lo, hi, ctok := n.compressible()
	nudge := false
	switch {
	case float64(T) >= 0.75*L && ctok >= 5000:
		nudge = true
	case !n.st.nudged && float64(T) >= 0.45*L && ctok >= 50000:
		nudge = true
	case n.st.nudged && T-n.st.lastNudge >= 22500 && ctok >= 50000:
		nudge = true
	}
	if nudge && lo >= 0 {
		chars := 0
		for i := lo; i <= hi; i++ {
			chars += len(n.st.raw[i].PlainText())
		}
		if chars >= 5000 {
			items := renderForSummary(n.st.raw[lo:hi+1], 1500)
			text, ms := n.summarize(ctx, items, min(5000, max(300, ctok/8)))
			if text != "" {
				n.st.blocks = append(n.st.blocks, &noaBlock{lo: lo, hi: hi, tier: 1, text: text, live: true})
				n.stats["compressions"]++
				extraCalls++
				extraIn += T
				extraOut += tokens.Estimate(text)
				fg += float64(n.lat.Of(0, tokens.Estimate(text)).Microseconds()) / 1000
				_ = ms
			}
			n.st.nudged = true
			n.st.lastNudge = T
		}
		n.escalate(ctx, 1, 5, &fg, &extraCalls, &extraIn, &extraOut, T)
		n.escalate(ctx, 2, 10, &fg, &extraCalls, &extraIn, &extraOut, T)
	}
	view, total := n.view(false)
	if float64(total) >= 0.95*L {
		view, total = n.view(true)
		n.stats["truncations"]++
	}
	if total > n.capacity {
		n.stats["overflows"]++
		return View{Overflow: true, ForegroundMS: fg, ExtraCalls: extraCalls, ExtraInput: extraIn, ExtraOutput: extraOut}, nil
	}
	cx := n.contextOf("", view)
	v, err := viewOf(cx)
	v.ForegroundMS = fg
	v.Tokens = total
	v.ExtraCalls, v.ExtraInput, v.ExtraOutput = extraCalls, extraIn, extraOut
	v.Context = cx
	return v, err
}

func (n *noa) escalate(ctx context.Context, tier, at int, fg *float64, calls, in, out *int, T int) {
	var live []*noaBlock
	for _, b := range n.st.blocks {
		if b.live && b.tier == tier {
			live = append(live, b)
		}
	}
	if len(live) < at {
		return
	}
	group := live[:at]
	var items []engine.SummaryInputItem
	for _, b := range group {
		items = append(items, engine.SummaryInputItem{ID: fmt.Sprintf("b%d-%d", b.lo, b.hi), Role: "summary", Text: b.text, Lo: int64(b.lo), Hi: int64(b.hi)})
	}
	n.summ.setFG(true)
	res, err := n.summ.Summarize(ctx, engine.SummarizeInput{Level: tier + 1, Items: items, MaxTokens: 3000})
	n.summ.setFG(false)
	if err != nil {
		return
	}
	text := res.Text + factsBlock(res.Facts)
	nb := &noaBlock{lo: group[0].lo, hi: group[len(group)-1].hi, tier: tier + 1, text: text, live: true}
	for _, b := range n.st.blocks {
		if b.live && b.lo >= nb.lo && b.hi <= nb.hi {
			b.live = false
		}
	}
	n.st.blocks = append(n.st.blocks, nb)
	n.stats[fmt.Sprintf("tier%d_blocks", tier+1)]++
	*calls++
	*in += T
	*out += tokens.Estimate(text)
	*fg += float64(n.lat.Of(0, tokens.Estimate(text)).Microseconds()) / 1000
	_ = strings.TrimSpace
}
