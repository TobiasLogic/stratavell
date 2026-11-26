package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/TobiasLogic/stratavell/msg"
)

type readyResult struct {
	job  jobSpec
	node *SummaryNode
}

type pinItem struct {
	taskID      string
	sourceEvent string
	seq         int64
	text        string
	explicit    bool
	exemptEvent string
	tokens      int
}

type viewRecord struct {
	Frontier []string `json:"frontier"`
}

func (a *Agent) load(ctx context.Context) error {
	e := a.e
	var parent sql.NullString
	if err := e.db.R.QueryRowContext(ctx, "SELECT parent_id, head_seq FROM agents WHERE session_id=? AND id=?", a.s.id, a.id).Scan(&parent, &a.head); err != nil {
		return err
	}
	a.parent = parent.String
	var frontierJSON string
	err := e.db.R.QueryRowContext(ctx, "SELECT generation, frontier, boundary_seq, mask_seq FROM views WHERE session_id=? AND agent_id=? ORDER BY generation DESC LIMIT 1", a.s.id, a.id).Scan(&a.gen, &frontierJSON, &a.boundary, &a.maskSeq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var vr viewRecord
		if err := json.Unmarshal([]byte(frontierJSON), &vr); err != nil {
			return fmt.Errorf("engine: view record: %w", err)
		}
		for _, id := range vr.Frontier {
			n, err := e.loadSummary(ctx, e.db.R, id)
			if err != nil {
				return fmt.Errorf("engine: frontier node %s: %w", id, err)
			}
			a.frontier = append(a.frontier, n)
		}
	}
	rows, err := e.db.R.QueryContext(ctx, "SELECT "+eventCols+" FROM events WHERE session_id=? AND agent_id=? AND seq>? ORDER BY seq", a.s.id, a.id, a.boundary)
	if err != nil {
		return err
	}
	type pending struct {
		ev Event
		sp []storedPart
		sm storedMeta
	}
	var list []pending
	for rows.Next() {
		ev, sp, sm, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return err
		}
		list = append(list, pending{ev, sp, sm})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range list {
		var parts []msg.Part
		if p.sm.Oversize {
			parts = make([]msg.Part, len(p.sp))
			for i, sp := range p.sp {
				parts[i] = sp.Part
			}
		} else {
			parts, err = e.resolveParts(ctx, e.db.R, p.sp)
			if err != nil {
				return fmt.Errorf("engine: restoring %s: %w", p.ev.ID, err)
			}
		}
		it := &item{ev: p.ev, parts: parts, meta: p.sm, escape: p.ev.Role == msg.RoleTool && needsEscape(parts)}
		for _, path := range p.sm.Host.Paths {
			it.primary = append(it.primary, path)
		}
		m := msg.Message{Role: p.ev.Role, Parts: parts}
		if p.ev.Role == msg.RoleTool && len(it.primary) == 0 {
			if c, ok := a.calls[p.ev.ToolCallID]; ok {
				it.primary = dedupeStrings(pathsFromInput(c.input))
			}
		}
		if err := a.pairing.Step(m); err != nil {
			return fmt.Errorf("engine: replaying pairing at %s: %w", p.ev.ID, err)
		}
		switch p.ev.Role {
		case msg.RoleAssistant:
			for _, c := range m.ToolCalls() {
				a.calls[c.ToolCallID] = openCall{eventID: p.ev.ID, tool: c.ToolName, input: c.Input}
				it.callIDs = append(it.callIDs, c.ToolCallID)
			}
		case msg.RoleTool:
			delete(a.calls, p.ev.ToolCallID)
		}
		a.trackFiles(it)
		a.raw = append(a.raw, it)
	}
	if err := a.loadFileIndex(ctx); err != nil {
		return err
	}
	if err := a.loadReady(ctx); err != nil {
		return err
	}
	return a.refreshPinned(ctx)
}

func (a *Agent) loadFileIndex(ctx context.Context) error {
	rows, err := a.e.db.R.QueryContext(ctx, "SELECT path, op, MAX(seq), event_id, content_hash FROM paths WHERE session_id=? AND agent_id=? AND op IN ('read','write') GROUP BY path, op", a.s.id, a.id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path, op, evID, fh string
		var seq int64
		if err := rows.Scan(&path, &op, &seq, &evID, &fh); err != nil {
			return err
		}
		if op == "write" {
			if seq > a.lastWrite[path] {
				a.lastWrite[path] = seq
			}
		} else if seq > a.lastRead[path].seq {
			a.lastRead[path] = readInfo{seq: seq, eventID: evID, fileHash: fh}
		}
	}
	return rows.Err()
}

func (a *Agent) loadReady(ctx context.Context) error {
	rows, err := a.e.db.R.QueryContext(ctx, "SELECT j.id, j.kind, j.level, j.base_generation, j.seq_lo, j.seq_hi, j.sources, j.summary_id FROM jobs j WHERE j.session_id=? AND j.agent_id=? AND j.state='done' AND j.summary_id IS NOT NULL", a.s.id, a.id)
	if err != nil {
		return err
	}
	type row struct {
		js  jobSpec
		sid string
	}
	var list []row
	for rows.Next() {
		var js jobSpec
		var sources string
		var sid sql.NullString
		if err := rows.Scan(&js.ID, &js.Kind, &js.Level, &js.BaseGen, &js.Lo, &js.Hi, &sources, &sid); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal([]byte(sources), &js.Sources)
		js.Session, js.Agent = a.s.id, a.id
		list = append(list, row{js, sid.String})
	}
	rows.Close()
	for _, r := range list {
		n, err := a.e.loadSummary(ctx, a.e.db.R, r.sid)
		if err != nil {
			continue
		}
		if n.State != "pending" {
			continue
		}
		a.ready[r.js.ID] = &readyResult{job: r.js, node: n}
	}
	return nil
}

func (a *Agent) frontierIDs() []string {
	ids := make([]string, len(a.frontier))
	for i, n := range a.frontier {
		ids[i] = n.ID
	}
	return ids
}

type publishChange struct {
	frontier   []*SummaryNode
	boundary   int64
	maskSeq    int64
	reason     string
	published  []string
	stale      []string
	superseded []string
	jobsDone   []string
	newNodes   []*SummaryNode
}

func (a *Agent) commitView(ctx context.Context, ch publishChange) error {
	ids := make([]string, len(ch.frontier))
	for i, n := range ch.frontier {
		ids[i] = n.ID
	}
	vr, _ := json.Marshal(viewRecord{Frontier: ids})
	gen := a.gen + 1
	err := a.e.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, n := range ch.newNodes {
			if err := a.e.insertSummary(ctx, tx, n); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO views(session_id,agent_id,generation,created_at,reason,frontier,boundary_seq,mask_seq,policy_version) VALUES(?,?,?,?,?,?,?,?,?)",
			a.s.id, a.id, gen, a.e.now(), ch.reason, string(vr), ch.boundary, ch.maskSeq, ConfigVersion); err != nil {
			return err
		}
		for _, id := range ch.published {
			if _, err := tx.ExecContext(ctx, "UPDATE summaries SET state='published' WHERE id=?", id); err != nil {
				return err
			}
		}
		for _, id := range ch.superseded {
			if _, err := tx.ExecContext(ctx, "UPDATE summaries SET state='superseded' WHERE id=?", id); err != nil {
				return err
			}
		}
		for _, id := range ch.stale {
			if _, err := tx.ExecContext(ctx, "UPDATE summaries SET state='stale', validation=CASE WHEN validation='valid' THEN 'stale' ELSE validation END WHERE id=?", id); err != nil {
				return err
			}
		}
		for _, id := range ch.jobsDone {
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state='published' WHERE id=?", id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sameFrontier := len(ch.frontier) == len(a.frontier)
	if sameFrontier {
		for i := range ch.frontier {
			if ch.frontier[i].ID != a.frontier[i].ID {
				sameFrontier = false
				break
			}
		}
	}
	a.gen = gen
	a.frontier = ch.frontier
	if ch.boundary > a.boundary {
		cut := 0
		for cut < len(a.raw) && a.raw[cut].ev.Seq <= ch.boundary {
			cut++
		}
		a.raw = append([]*item(nil), a.raw[cut:]...)
	}
	a.boundary = ch.boundary
	a.maskSeq = ch.maskSeq
	a.e.metrics.inc("generations", 1)
	if sameFrontier && a.pinnedInit {
		return a.refreshExempt(ctx)
	}
	return a.refreshPinned(ctx)
}

func (a *Agent) refreshExempt(ctx context.Context) error {
	items, err := a.e.liveTaskItems(ctx, a.e.db.R, a.s.id, a.id, []string{kindFailure})
	if err != nil {
		return err
	}
	kept := a.pinned[:0:0]
	for _, p := range a.pinned {
		if p.exemptEvent == "" {
			kept = append(kept, p)
		}
	}
	for _, ti := range items {
		if ti.Status == statusOpen && ti.SourceEvent != "" {
			kept = append(kept, pinItem{taskID: ti.ID, exemptEvent: ti.SourceEvent})
		}
	}
	a.pinned = kept
	return nil
}

func (a *Agent) publishReady(ctx context.Context) (int, error) {
	if len(a.ready) == 0 {
		return 0, nil
	}
	frontier := append([]*SummaryNode(nil), a.frontier...)
	boundary := a.boundary
	ch := publishChange{reason: "publish", maskSeq: a.maskSeq}
	used := map[string]bool{}
	keys := make([]string, 0, len(a.ready))
	for k := range a.ready {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ri, rj := a.ready[keys[i]], a.ready[keys[j]]
		if ri.node.Lo != rj.node.Lo {
			return ri.node.Lo < rj.node.Lo
		}
		return ri.node.Level < rj.node.Level
	})
	for progress := true; progress; {
		progress = false
		for _, k := range keys {
			if used[k] {
				continue
			}
			r := a.ready[k]
			n := r.node
			switch {
			case r.job.Kind == jobLeaf && n.Lo == boundary+1:
				frontier = append(frontier, n)
				boundary = n.Hi
			case r.job.Kind == jobLeaf && n.Lo <= boundary:
				if i := findExact(frontier, n.Lo, n.Hi); i >= 0 && frontier[i].Kind == KindPointer && a.pol.Compaction.UpgradePointers {
					ch.superseded = append(ch.superseded, frontier[i].ID)
					frontier[i] = n
				} else {
					ch.stale = append(ch.stale, n.ID)
					ch.jobsDone = append(ch.jobsDone, r.job.ID)
					used[k] = true
					progress = true
					continue
				}
			case r.job.Kind == jobCondense:
				i, j := findSources(frontier, r.job.Sources)
				if i < 0 {
					if n.Hi <= boundary {
						ch.stale = append(ch.stale, n.ID)
						ch.jobsDone = append(ch.jobsDone, r.job.ID)
						used[k] = true
						progress = true
					}
					continue
				}
				for _, old := range frontier[i : j+1] {
					ch.superseded = append(ch.superseded, old.ID)
				}
				nf := append([]*SummaryNode(nil), frontier[:i]...)
				nf = append(nf, n)
				frontier = append(nf, frontier[j+1:]...)
			default:
				continue
			}
			ch.published = append(ch.published, n.ID)
			ch.jobsDone = append(ch.jobsDone, r.job.ID)
			used[k] = true
			progress = true
		}
	}
	if len(ch.jobsDone) == 0 {
		return 0, nil
	}
	ch.frontier = frontier
	ch.boundary = boundary
	if len(ch.published) > 0 && a.pol.Masking.Mode == MaskEpoch {
		ch.maskSeq = maxI64(ch.maskSeq, a.maskTarget(boundary))
	}
	if len(ch.published) == 0 {
		ch.frontier, ch.boundary = a.frontier, a.boundary
	}
	if err := a.commitView(ctx, ch); err != nil {
		return 0, err
	}
	for k := range used {
		delete(a.ready, k)
	}
	a.e.metrics.inc("summaries_published", float64(len(ch.published)))
	a.e.metrics.inc("summaries_stale", float64(len(ch.stale)))
	return len(ch.published), nil
}

func findExact(fr []*SummaryNode, lo, hi int64) int {
	for i, n := range fr {
		if n.Lo == lo && n.Hi == hi {
			return i
		}
	}
	return -1
}

func findSources(fr []*SummaryNode, ids []string) (int, int) {
	if len(ids) == 0 {
		return -1, -1
	}
	for i, n := range fr {
		if n.ID != ids[0] {
			continue
		}
		if i+len(ids) > len(fr) {
			return -1, -1
		}
		for k, id := range ids {
			if fr[i+k].ID != id {
				return -1, -1
			}
		}
		return i, i + len(ids) - 1
	}
	return -1, -1
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (a *Agent) refreshPinned(ctx context.Context) error {
	all, err := a.e.liveTaskItems(ctx, a.e.db.R, a.s.id, a.id, nil)
	if err != nil {
		return err
	}
	a.taskSnap, a.taskSnapTok, a.taskSnapAt = "", 0, a.e.now()
	if a.pol.TaskState && a.pol.Cache.Layout != LayoutSpec && a.gen > 0 {
		budget := max(400, int(a.pol.Budget.TaskStateFraction*float64(a.capacity())))
		a.taskSnap, a.taskSnapTok = a.renderTaskState(all, budget)
	}
	var items []TaskItem
	for _, t := range all {
		if t.Kind == kindConstraint || t.Kind == kindDirective || t.Kind == kindFailure {
			items = append(items, t)
		}
	}
	visible := map[string]bool{}
	for _, it := range a.raw {
		if !it.meta.Oversize {
			visible[it.ev.ID] = true
		}
	}
	var pins []pinItem
	for _, ti := range items {
		switch ti.Kind {
		case kindFailure:
			if ti.Status == statusOpen && ti.SourceEvent != "" {
				pins = append(pins, pinItem{taskID: ti.ID, exemptEvent: ti.SourceEvent})
			}
		case kindConstraint, kindDirective:
			if ti.Kind == kindDirective && !a.pol.PinDirective {
				continue
			}
			if ti.SourceEvent != "" && visible[ti.SourceEvent] && ti.Owner == a.id {
				continue
			}
			pins = append(pins, pinItem{taskID: ti.ID, sourceEvent: ti.SourceEvent, text: ti.Text, explicit: ti.Kind == kindConstraint, seq: ti.Seq})
		}
	}
	sort.SliceStable(pins, func(i, j int) bool { return pins[i].seq < pins[j].seq })
	a.pinned = pins
	a.pinnedInit = true
	return nil
}

func (a *Agent) maskTarget(boundary int64) int64 {
	start := a.recentStart(boundary)
	if start <= 0 {
		return 0
	}
	return start - 1
}
