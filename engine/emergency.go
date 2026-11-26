package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/TobiasLogic/stratavell/msg"
)

func (a *Agent) archiveRange(ctx context.Context, from, to int64, reason string) (int, error) {
	var exact [][2]int64
	for _, j := range a.leafJobs {
		exact = append(exact, [2]int64{j.Lo, j.Hi})
	}
	for _, r := range a.ready {
		if r.job.Kind == jobLeaf {
			exact = append(exact, [2]int64{r.job.Lo, r.job.Hi})
		}
	}
	chunks := a.chunksBetween(from, to, a.pol.Compaction.LeafChunkTokens, 1, exact)
	if len(chunks) == 0 {
		return 0, nil
	}
	frontier := append([]*SummaryNode(nil), a.frontier...)
	var nodes []*SummaryNode
	boundary := a.boundary
	for _, c := range chunks {
		if a.raw[c.lo].ev.Seq != boundary+1 {
			break
		}
		j := a.leafSpec(c)
		n := a.e.buildPointerNode(j, reason)
		n.State = "published"
		nodes = append(nodes, n)
		frontier = append(frontier, n)
		boundary = n.Hi
	}
	if len(nodes) == 0 {
		return 0, nil
	}
	mask := a.maskSeq
	if a.pol.Masking.Mode == MaskEpoch {
		mask = maxI64(mask, a.maskTarget(boundary))
	}
	err := a.commitView(ctx, publishChange{frontier: frontier, boundary: boundary, maskSeq: mask, reason: reason, newNodes: nodes})
	if err != nil {
		return 0, err
	}
	for _, n := range nodes {
		a.e.idx.enqueueSummary(n.ID)
	}
	a.e.metrics.inc("pointer_nodes", float64(len(nodes)))
	return len(nodes), nil
}

func (a *Agent) condensePointer(ctx context.Context, nodes []*SummaryNode, reason string) error {
	j := a.condenseSpec(nodes)
	n := a.e.buildPointerNode(j, reason)
	n.State = "published"
	i, k := findSources(a.frontier, j.Sources)
	if i < 0 {
		return fmt.Errorf("engine: condense sources not in frontier")
	}
	nf := append([]*SummaryNode(nil), a.frontier[:i]...)
	nf = append(nf, n)
	nf = append(nf, a.frontier[k+1:]...)
	ch := publishChange{frontier: nf, boundary: a.boundary, maskSeq: a.maskSeq, reason: reason, newNodes: []*SummaryNode{n}}
	for _, old := range nodes {
		ch.superseded = append(ch.superseded, old.ID)
	}
	if err := a.commitView(ctx, ch); err != nil {
		return err
	}
	a.e.idx.enqueueSummary(n.ID)
	a.e.metrics.inc("pointer_nodes", 1)
	return nil
}

func (a *Agent) emergency(ctx context.Context, p *parts, fixed int, req AssembleRequest, rep *Report) error {
	capTok := float64(a.capacity())
	th := a.pol.Thresholds
	over := func() bool { return float64(a.calib.Adjust(p.total)) >= th.Emergency*capTok }
	start := time.Now()
	defer func() { a.e.metrics.observe("emergency_ms", since(start)) }()
	a.e.metrics.inc("emergencies", 1)
	remeasure := func() error {
		q, err := a.measure(ctx, fixed, req)
		if err != nil {
			return err
		}
		*p = q
		return nil
	}
	setMask := func(target int64, label string) error {
		if target <= a.maskSeq {
			return nil
		}
		if err := a.commitView(ctx, publishChange{frontier: a.frontier, boundary: a.boundary, maskSeq: target, reason: label}); err != nil {
			return err
		}
		rep.Actions = append(rep.Actions, label)
		return remeasure()
	}
	if err := setMask(a.maskTarget(a.boundary), "emergency_mask"); err != nil {
		return err
	}
	for round := 0; over() && round < 16; round++ {
		before := p.total
		recent := a.recentStart(a.boundary)
		if n, err := a.archiveRange(ctx, a.boundary+1, recent-1, "emergency_archive"); err != nil {
			return err
		} else if n > 0 {
			rep.Actions = append(rep.Actions, fmt.Sprintf("emergency_archive:%d", n))
			if err := remeasure(); err != nil {
				return err
			}
			continue
		}
		if len(a.frontier) >= 2 && float64(p.sumTok) > 0.04*capTok {
			k := min(len(a.frontier), max(2, a.pol.Compaction.CondenseFanin))
			if err := a.condensePointer(ctx, a.frontier[:k], "emergency_condense"); err != nil {
				return err
			}
			rep.Actions = append(rep.Actions, fmt.Sprintf("emergency_condense:%d", k))
			if err := remeasure(); err != nil {
				return err
			}
			if p.total < before {
				continue
			}
		}
		floor := a.beforeLastAssistants(1)
		if floor > a.maskSeq {
			if err := setMask(floor, "emergency_mask_deep"); err != nil {
				return err
			}
			if p.total < before {
				continue
			}
		}
		if floor > a.boundary {
			if n, err := a.archiveRange(ctx, a.boundary+1, floor, "emergency_archive_recent"); err != nil {
				return err
			} else if n > 0 {
				rep.Actions = append(rep.Actions, fmt.Sprintf("emergency_archive_recent:%d", n))
				if err := remeasure(); err != nil {
					return err
				}
				continue
			}
		}
		if !a.squeeze {
			a.squeeze = true
			a.emergencyMask = a.head
			rep.Actions = append(rep.Actions, "emergency_squeeze")
			if err := remeasure(); err != nil {
				return err
			}
			continue
		}
		if p.total >= before {
			break
		}
	}
	return nil
}

func (a *Agent) beforeLastAssistants(k int) int64 {
	seen := 0
	for i := len(a.raw) - 1; i >= 0; i-- {
		if a.raw[i].ev.Role == msg.RoleAssistant {
			seen++
			if seen == k {
				if i == 0 {
					return a.boundary
				}
				return a.raw[i-1].ev.Seq
			}
		}
	}
	return a.boundary
}

func (a *Agent) handleFailures(ctx context.Context, rep *Report) error {
	if len(a.failedJobs) == 0 {
		return nil
	}
	failed := a.failedJobs
	a.failedJobs = nil
	usage := float64(a.calib.Adjust(a.estimateUsage())) / float64(a.capacity())
	for _, j := range failed {
		k := [2]int64{j.Lo, j.Hi}
		a.failCount[k]++
		if j.Kind != jobLeaf {
			continue
		}
		if j.Lo != a.boundary+1 {
			continue
		}
		if usage >= a.pol.Thresholds.Aggressive || a.failCount[k] >= 2 {
			n, err := a.archiveRange(ctx, j.Lo, j.Hi, "fallback_after_failure")
			if err != nil {
				return err
			}
			if n > 0 {
				rep.Actions = append(rep.Actions, "fallback_pointer")
				a.e.metrics.inc("fallbacks", 1)
			}
		}
	}
	return nil
}

func (a *Agent) syncCompact(ctx context.Context, total int) (int, error) {
	summ := a.e.getSummarizer()
	from := a.boundary + 1
	to := a.recentStart(a.boundary) - 1
	if to < from {
		return 0, nil
	}
	need := total - int(a.pol.Thresholds.Target*float64(a.capacity()))
	published := 0
	freed := 0
	for freed < need {
		chunks := a.chunksBetween(a.boundary+1, to, a.pol.Compaction.LeafChunkTokens, a.pol.Compaction.MinChunkTokens, nil)
		if len(chunks) == 0 {
			break
		}
		c := chunks[0]
		if a.raw[c.lo].ev.Seq != a.boundary+1 {
			break
		}
		j := a.leafSpec(c)
		cctx, cancel := context.WithTimeout(ctx, a.pol.Compaction.JobTimeout.D())
		t0 := time.Now()
		out, err := summ.Summarize(cctx, j.Input)
		cancel()
		a.e.metrics.observe("summarize_ms", since(t0))
		a.pred.observeLatency(time.Since(t0).Seconds())
		var n *SummaryNode
		if err == nil {
			a.e.metrics.inc("summarizer_input_tokens", float64(out.InputTokens))
			a.e.metrics.inc("summarizer_output_tokens", float64(out.OutputTokens))
			n, err = a.e.buildSemanticNode(j, out)
		}
		if err != nil {
			n = a.e.buildPointerNode(j, "sync summary failed: "+err.Error())
			a.e.metrics.inc("fallbacks", 1)
		}
		n.State = "published"
		frontier := append(append([]*SummaryNode(nil), a.frontier...), n)
		mask := a.maskSeq
		if a.pol.Masking.Mode == MaskEpoch {
			mask = maxI64(mask, a.maskTarget(n.Hi))
		}
		if err := a.commitView(ctx, publishChange{frontier: frontier, boundary: n.Hi, maskSeq: mask, reason: "sync_compact", newNodes: []*SummaryNode{n}}); err != nil {
			return published, err
		}
		a.e.idx.enqueueSummary(n.ID)
		freed += c.tok - n.Tokens
		published++
	}
	if nodes := a.condenseCandidate(); len(nodes) >= 2 {
		j := a.condenseSpec(nodes)
		cctx, cancel := context.WithTimeout(ctx, a.pol.Compaction.JobTimeout.D())
		out, err := summ.Summarize(cctx, j.Input)
		cancel()
		var n *SummaryNode
		if err == nil {
			n, err = a.e.buildSemanticNode(j, out)
		}
		if err != nil {
			n = a.e.buildPointerNode(j, "sync condense failed")
		}
		n.State = "published"
		i, k := findSources(a.frontier, j.Sources)
		if i >= 0 {
			nf := append([]*SummaryNode(nil), a.frontier[:i]...)
			nf = append(nf, n)
			nf = append(nf, a.frontier[k+1:]...)
			ch := publishChange{frontier: nf, boundary: a.boundary, maskSeq: a.maskSeq, reason: "sync_condense", newNodes: []*SummaryNode{n}}
			for _, old := range nodes {
				ch.superseded = append(ch.superseded, old.ID)
			}
			if err := a.commitView(ctx, ch); err != nil {
				return published, err
			}
			a.e.idx.enqueueSummary(n.ID)
		}
	}
	return published, nil
}
