package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/internal/textproc"
	"github.com/TobiasLogic/palimpsest/msg"
)

type SearchFilters struct {
	Kinds     []string `json:"kinds,omitempty"`
	Roles     []string `json:"roles,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	Path      string   `json:"path,omitempty"`
	SeqFrom   int64    `json:"seq_from,omitempty"`
	SeqTo     int64    `json:"seq_to,omitempty"`
	Since     int64    `json:"since,omitempty"`
	Until     int64    `json:"until,omitempty"`
	AllAgents bool     `json:"all_agents,omitempty"`
}

type SearchHit struct {
	ID         string  `json:"id"`
	Kind       string  `json:"kind"`
	Agent      string  `json:"agent"`
	Seq        int64   `json:"seq"`
	Role       string  `json:"role,omitempty"`
	Tool       string  `json:"tool,omitempty"`
	Snippet    string  `json:"snippet"`
	Score      float64 `json:"score"`
	Provenance string  `json:"provenance"`
	InContext  bool    `json:"in_context"`
	Stale      bool    `json:"stale,omitempty"`
	Tokens     int     `json:"tokens"`
}

var (
	reQueryTok = regexp.MustCompile(`[A-Za-z0-9_]+`)
	reIDTok    = regexp.MustCompile(`\b(?:ev|sm|tk)_[0-9A-Z]{26}\b`)
	rePathTok  = regexp.MustCompile(`[A-Za-z0-9_\-./]+\.[A-Za-z0-9]{1,8}`)
	stopwords  = map[string]bool{"the": true, "a": true, "an": true, "of": true, "to": true, "in": true, "and": true, "or": true, "is": true, "was": true, "what": true, "which": true, "for": true, "on": true, "did": true, "we": true, "it": true, "that": true, "this": true, "with": true, "how": true, "why": true, "where": true, "when": true, "be": true, "are": true, "do": true, "does": true, "i": true, "you": true, "me": true, "my": true, "our": true, "about": true, "from": true, "at": true, "by": true, "as": true, "if": true, "then": true, "there": true}
)

func ftsQuery(q string, max int) string {
	var terms []string
	seen := map[string]bool{}
	for _, t := range reQueryTok.FindAllString(q, -1) {
		lt := strings.ToLower(t)
		if len(lt) < 2 || stopwords[lt] || seen[lt] {
			continue
		}
		seen[lt] = true
		terms = append(terms, `"`+lt+`"`)
		for _, sub := range textproc.SplitIdent(t) {
			if sub != lt && !seen[sub] && len(sub) > 2 && !stopwords[sub] {
				seen[sub] = true
				terms = append(terms, `"`+sub+`"`)
			}
		}
		if len(terms) >= max {
			break
		}
	}
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

type visibility struct {
	session     string
	agent       string
	descendants map[string]bool
	grants      map[string]bool
}

func (a *Agent) visibility(ctx context.Context) (*visibility, error) {
	v := &visibility{session: a.s.id, agent: a.id, descendants: map[string]bool{a.id: true}, grants: map[string]bool{}}
	rows, err := a.e.db.R.QueryContext(ctx, "SELECT id, COALESCE(parent_id,'') FROM agents WHERE session_id=?", a.s.id)
	if err != nil {
		return nil, err
	}
	children := map[string][]string{}
	for rows.Next() {
		var id, p string
		if err := rows.Scan(&id, &p); err != nil {
			rows.Close()
			return nil, err
		}
		children[p] = append(children[p], id)
	}
	rows.Close()
	stack := []string{a.id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range children[cur] {
			if !v.descendants[c] {
				v.descendants[c] = true
				stack = append(stack, c)
			}
		}
	}
	grows, err := a.e.db.R.QueryContext(ctx, "SELECT ref_id FROM grants WHERE session_id=? AND agent_id=?", a.s.id, a.id)
	if err != nil {
		return nil, err
	}
	for grows.Next() {
		var r string
		if grows.Scan(&r) == nil {
			v.grants[r] = true
		}
	}
	grows.Close()
	return v, nil
}

func (v *visibility) allows(session, agent, scope, id string) bool {
	if session != v.session {
		return false
	}
	return v.descendants[agent] || scope == "shared" || v.grants[id]
}

type candidate struct {
	refKind string
	refID   string
	agent   string
	scope   string
	seq     int64
	ts      int64
	tool    string
	rel     int
	snippet string
	score   float64
}

func (a *Agent) Search(ctx context.Context, query string, f SearchFilters, limit int) ([]SearchHit, error) {
	start := time.Now()
	defer func() { a.e.metrics.observe("retrieval_ms", since(start)) }()
	a.e.metrics.inc("retrievals", 1)
	if limit <= 0 {
		limit = a.pol.Retrieval.DefaultLimit
	}
	limit = min(limit, a.pol.Retrieval.MaxLimit)
	lists, err := a.gather(ctx, query, f)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	inCtx := a.inContextSetLocked()
	head := a.head
	a.mu.Unlock()
	return a.rank(ctx, query, f, limit, lists, inCtx, head)
}

func (a *Agent) searchLocked(ctx context.Context, query string, f SearchFilters, limit int) ([]SearchHit, error) {
	lists, err := a.gather(ctx, query, f)
	if err != nil {
		return nil, err
	}
	return a.rank(ctx, query, f, limit, lists, a.inContextSetLocked(), a.head)
}

func (a *Agent) gather(ctx context.Context, query string, f SearchFilters) (map[string][]candidate, error) {
	vis, err := a.visibility(ctx)
	if err != nil {
		return nil, err
	}
	lists := map[string][]candidate{}
	if q := ftsQuery(query, 24); q != "" {
		sid := sessionToken(a.s.id)
		rows, err := a.e.db.R.QueryContext(ctx, `SELECT d.ref_kind, d.ref_id, d.agent_id, d.scope, d.seq, d.ts, d.tool, d.reliability,
snippet(docs_fts, 0, '', '', ' ... ', ?), bm25(docs_fts, 1.0, 0.4, 0.0)
FROM docs_fts JOIN docs d ON d.id = docs_fts.rowid
WHERE docs_fts MATCH ? ORDER BY bm25(docs_fts, 1.0, 0.4, 0.0) LIMIT 200`, a.pol.Retrieval.SnippetTokens, "sid:"+sid+" AND ("+q+")")
		if err != nil {
			return nil, fmt.Errorf("engine: fts query: %w", err)
		}
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.refKind, &c.refID, &c.agent, &c.scope, &c.seq, &c.ts, &c.tool, &c.rel, &c.snippet, &c.score); err != nil {
				rows.Close()
				return nil, err
			}
			if !vis.allows(a.s.id, c.agent, c.scope, c.refID) {
				continue
			}
			lists["fts"] = append(lists["fts"], c)
		}
		rows.Close()
	}
	if err := a.symbolHits(ctx, query, vis, lists); err != nil {
		return nil, err
	}
	if err := a.pathHits(ctx, query, f.Path, vis, lists); err != nil {
		return nil, err
	}
	for _, id := range reIDTok.FindAllString(query, 8) {
		if c, ok := a.idCandidate(ctx, id, vis); ok {
			lists["id"] = append(lists["id"], c)
		}
	}
	if a.e.embedder != nil {
		if err := a.vectorHits(ctx, query, vis, lists); err != nil {
			a.e.metrics.inc("embedding_failures", 1)
		}
	}
	return lists, nil
}

func (a *Agent) rank(ctx context.Context, query string, f SearchFilters, limit int, lists map[string][]candidate, inCtx map[string]bool, head int64) ([]SearchHit, error) {
	byID := map[string]*candidate{}
	clists := map[string][]Candidate{}
	for name, l := range lists {
		seen := map[string]bool{}
		rank := 0
		for _, c := range l {
			if seen[c.refID] {
				continue
			}
			seen[c.refID] = true
			rank++
			if cur, ok := byID[c.refID]; !ok || (cur.snippet == "" && c.snippet != "") {
				cc := c
				byID[c.refID] = &cc
			}
			clists[name] = append(clists[name], Candidate{List: name, Rank: rank, RefKind: c.refKind, RefID: c.refID, Agent: c.agent, Seq: c.seq, TS: c.ts, Tool: c.tool, Reliability: c.rel, OwnAgent: c.agent == a.id, HeadSeq: head})
		}
	}
	stale := map[string]bool{}
	for id, c := range byID {
		if c.refKind == "event" && c.agent == a.id && looksLikeRead(c.tool) && a.isStale(c) {
			stale[id] = true
		}
	}
	for name, l := range clists {
		for i := range l {
			l[i].Stale = stale[l[i].RefID]
		}
		clists[name] = l
	}
	scores := a.e.ranker.Rank(query, clists)
	var hits []SearchHit
	for id, s := range scores {
		c := byID[id]
		if c == nil || !a.passes(ctx, c, f) {
			continue
		}
		h := SearchHit{ID: id, Kind: c.refKind, Agent: c.agent, Seq: c.seq, Tool: c.tool, Snippet: textproc.Clip(strings.Join(strings.Fields(c.snippet), " "), 320), Score: s, InContext: inCtx[id], Stale: stale[id]}
		h.Provenance = provenanceOf(c)
		hits = append(hits, h)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].ID < hits[j].ID
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	for i := range hits {
		if hits[i].Snippet == "" {
			hits[i].Snippet = a.snippetFor(ctx, hits[i].ID, hits[i].Kind)
		}
	}
	if len(hits) > 0 {
		a.e.metrics.inc("retrieval_hits", 1)
	}
	return hits, nil
}

func provenanceOf(c *candidate) string {
	switch {
	case c.refKind == "summary":
		return "derived summary"
	case c.rel == 3 && c.tool != "":
		return "tool observation"
	case c.rel == 3:
		return "user message"
	default:
		return "assistant message (model-generated)"
	}
}

func (a *Agent) passes(ctx context.Context, c *candidate, f SearchFilters) bool {
	if !f.AllAgents && c.agent != a.id && c.scope != "shared" {
		if c.refKind == "summary" {
			return false
		}
	}
	if len(f.Kinds) > 0 && !contains(f.Kinds, c.refKind) {
		return false
	}
	if len(f.Tools) > 0 && !contains(f.Tools, c.tool) {
		return false
	}
	if f.SeqFrom > 0 && c.seq < f.SeqFrom || f.SeqTo > 0 && c.seq > f.SeqTo {
		return false
	}
	if f.Since > 0 && c.ts < f.Since || f.Until > 0 && c.ts > f.Until {
		return false
	}
	if len(f.Roles) > 0 && c.refKind == "event" {
		var role string
		_ = a.e.db.R.QueryRowContext(ctx, "SELECT role FROM events WHERE id=?", c.refID).Scan(&role)
		if !contains(f.Roles, role) {
			return false
		}
	}
	if f.Path != "" && c.refKind == "event" {
		var n int
		_ = a.e.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM paths WHERE event_id=? AND (path=? OR path LIKE ?)", c.refID, f.Path, "%"+f.Path).Scan(&n)
		if n == 0 {
			return false
		}
	}
	return true
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func (a *Agent) symbolHits(ctx context.Context, query string, vis *visibility, lists map[string][]candidate) error {
	var names []string
	for _, t := range reQueryTok.FindAllString(query, 16) {
		if len(t) >= 3 && (strings.Contains(t, "_") || strings.ToLower(t) != t) {
			names = append(names, strings.ToLower(t))
		}
	}
	for _, n := range names {
		rows, err := a.e.db.R.QueryContext(ctx, "SELECT s.event_id, s.agent_id, s.seq, e.scope, e.ts, COALESCE(e.tool_name,''), e.role FROM symbols s JOIN events e ON e.id=s.event_id WHERE s.session_id=? AND s.lname=? ORDER BY s.seq DESC LIMIT 20", a.s.id, n)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c candidate
			var role string
			if err := rows.Scan(&c.refID, &c.agent, &c.seq, &c.scope, &c.ts, &c.tool, &role); err != nil {
				rows.Close()
				return err
			}
			c.refKind = "event"
			c.rel = relFromRole(role)
			if vis.allows(a.s.id, c.agent, c.scope, c.refID) {
				lists["symbol"] = append(lists["symbol"], c)
			}
		}
		rows.Close()
	}
	return nil
}

func relFromRole(role string) int {
	if role == string(msg.RoleAssistant) {
		return 1
	}
	return 3
}

func (a *Agent) pathHits(ctx context.Context, query, path string, vis *visibility, lists map[string][]candidate) error {
	paths := rePathTok.FindAllString(query, 8)
	if path != "" {
		paths = append(paths, path)
	}
	for _, p := range paths {
		if !strings.ContainsAny(p, "/.") {
			continue
		}
		p = strings.TrimPrefix(p, "./")
		rows, err := a.e.db.R.QueryContext(ctx, "SELECT p.event_id, p.agent_id, p.seq, e.scope, e.ts, COALESCE(e.tool_name,''), e.role FROM paths p JOIN events e ON e.id=p.event_id WHERE p.session_id=? AND (p.path=? OR p.path LIKE ?) ORDER BY (p.op != 'mention') DESC, p.seq DESC LIMIT 20", a.s.id, p, "%/"+p)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c candidate
			var role string
			if err := rows.Scan(&c.refID, &c.agent, &c.seq, &c.scope, &c.ts, &c.tool, &role); err != nil {
				rows.Close()
				return err
			}
			c.refKind = "event"
			c.rel = relFromRole(role)
			if vis.allows(a.s.id, c.agent, c.scope, c.refID) {
				lists["path"] = append(lists["path"], c)
			}
		}
		rows.Close()
	}
	return nil
}

func (a *Agent) idCandidate(ctx context.Context, id string, vis *visibility) (candidate, bool) {
	var c candidate
	c.refID = id
	if strings.HasPrefix(id, "sm_") {
		var session string
		err := a.e.db.R.QueryRowContext(ctx, "SELECT session_id, agent_id, seq_hi, created_at FROM summaries WHERE id=?", id).Scan(&session, &c.agent, &c.seq, &c.ts)
		if err != nil || !vis.allows(session, c.agent, "private", id) {
			return c, false
		}
		c.refKind, c.rel, c.scope = "summary", 2, "private"
		return c, true
	}
	var session, role string
	err := a.e.db.R.QueryRowContext(ctx, "SELECT session_id, agent_id, seq, scope, ts, COALESCE(tool_name,''), role FROM events WHERE id=?", id).Scan(&session, &c.agent, &c.seq, &c.scope, &c.ts, &c.tool, &role)
	if err != nil || !vis.allows(session, c.agent, c.scope, id) {
		return c, false
	}
	c.refKind, c.rel = "event", relFromRole(role)
	return c, true
}

func (a *Agent) vectorHits(ctx context.Context, query string, vis *visibility, lists map[string][]candidate) error {
	ectx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	qv, err := a.e.embedder.Embed(ectx, []string{query})
	if err != nil || len(qv) != 1 {
		if err == nil {
			err = errors.New("embedder returned no vector")
		}
		return err
	}
	rows, err := a.e.db.R.QueryContext(ctx, "SELECT v.vec, d.ref_kind, d.ref_id, d.agent_id, d.scope, d.seq, d.ts, d.tool, d.reliability FROM vectors v JOIN docs d ON d.id=v.doc_id WHERE v.session_id=? LIMIT 50000", a.s.id)
	if err != nil {
		return err
	}
	defer rows.Close()
	type sc struct {
		c candidate
		s float64
	}
	var all []sc
	for rows.Next() {
		var b []byte
		var c candidate
		if err := rows.Scan(&b, &c.refKind, &c.refID, &c.agent, &c.scope, &c.seq, &c.ts, &c.tool, &c.rel); err != nil {
			return err
		}
		if !vis.allows(a.s.id, c.agent, c.scope, c.refID) {
			continue
		}
		all = append(all, sc{c, cosine(qv[0], decodeVec(b))})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].s > all[j].s })
	for i := 0; i < len(all) && i < 50; i++ {
		lists["vector"] = append(lists["vector"], all[i].c)
	}
	return rows.Err()
}

func looksLikeRead(tool string) bool {
	t := strings.ToLower(tool)
	return strings.Contains(t, "read") || strings.Contains(t, "view") || t == "cat" || strings.Contains(t, "open")
}

func (a *Agent) inContextSetLocked() map[string]bool {
	out := map[string]bool{}
	rs := a.newRenderState()
	for _, it := range a.raw {
		if it.meta.Oversize {
			continue
		}
		if it.ev.Seq <= rs.maskSeq && it.ev.Role == msg.RoleTool && !rs.exempt[it.ev.ID] {
			continue
		}
		out[it.ev.ID] = true
	}
	for _, n := range a.frontier {
		out[n.ID] = true
	}
	return out
}

func (a *Agent) isStale(c *candidate) bool {
	if c.refKind != "event" || c.agent != a.id {
		return false
	}
	var ct string
	var paths string
	if err := a.e.db.R.QueryRow("SELECT content_type, paths FROM events WHERE id=?", c.refID).Scan(&ct, &paths); err != nil {
		return false
	}
	if ct != string(msg.CTFileRead) {
		return false
	}
	var n int
	_ = a.e.db.R.QueryRow("SELECT COUNT(*) FROM paths p WHERE p.session_id=? AND p.op='write' AND p.seq > ? AND p.path IN (SELECT path FROM paths WHERE event_id=? AND op='read')", a.s.id, c.seq, c.refID).Scan(&n)
	return n > 0
}

func (a *Agent) snippetFor(ctx context.Context, id, kind string) string {
	if kind == "summary" {
		var t string
		_ = a.e.db.R.QueryRowContext(ctx, "SELECT text FROM summaries WHERE id=?", id).Scan(&t)
		return textproc.Clip(t, 300)
	}
	_, m, _, err := a.e.loadEvent(ctx, a.e.db.R, id)
	if err != nil {
		return ""
	}
	return textproc.Clip(strings.Join(strings.Fields(m.PlainText()), " "), 300)
}

func (a *Agent) checkEventAccess(ctx context.Context, id string) (Event, error) {
	vis, err := a.visibility(ctx)
	if err != nil {
		return Event{}, err
	}
	var session, agent, scope string
	err = a.e.db.R.QueryRowContext(ctx, "SELECT session_id, agent_id, scope FROM events WHERE id=?", id).Scan(&session, &agent, &scope)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Event{}, err
	}
	if !vis.allows(session, agent, scope, id) {
		return Event{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return Event{ID: id, SessionID: session, AgentID: agent, Scope: scope}, nil
}

func (a *Agent) GetEvent(ctx context.Context, id string) (Event, msg.Message, error) {
	if _, err := a.checkEventAccess(ctx, id); err != nil {
		return Event{}, msg.Message{}, err
	}
	ev, m, _, err := a.e.loadEvent(ctx, a.e.db.R, id)
	if err == nil {
		a.e.metrics.inc("exact_recoveries", 1)
	} else {
		a.e.metrics.inc("exact_recovery_failures", 1)
	}
	return ev, m, err
}

func (a *Agent) GetArtifact(ctx context.Context, ref string, offset, length int) ([]byte, int, error) {
	if length <= 0 || length > a.pol.Retrieval.ArtifactMaxLength {
		length = a.pol.Retrieval.ArtifactMaxLength
	}
	if offset < 0 {
		offset = 0
	}
	hash := ref
	if strings.HasPrefix(ref, "ev_") {
		var body []byte
		if _, err := a.checkEventAccess(ctx, ref); err != nil {
			return nil, 0, err
		}
		_, m, _, err := a.e.loadEvent(ctx, a.e.db.R, ref)
		if err != nil {
			return nil, 0, err
		}
		body = []byte(m.PlainText())
		return sliceBytes(body, offset, length), len(body), nil
	}
	vis, err := a.visibility(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := a.e.db.R.QueryContext(ctx, "SELECT r.event_id, r.agent_id, e.scope FROM blob_refs r JOIN events e ON e.id=r.event_id WHERE r.blob_hash=? AND r.session_id=?", hash, a.s.id)
	if err != nil {
		return nil, 0, err
	}
	ok := false
	for rows.Next() {
		var ev, agent, scope string
		if rows.Scan(&ev, &agent, &scope) == nil && vis.allows(a.s.id, agent, scope, ev) {
			ok = true
		}
	}
	rows.Close()
	if !ok {
		return nil, 0, fmt.Errorf("%w: artifact %s", ErrNotFound, short(hash))
	}
	data, err := getBlob(ctx, a.e, hash)
	if err != nil {
		return nil, 0, err
	}
	return sliceBytes(data, offset, length), len(data), nil
}

func sliceBytes(b []byte, off, n int) []byte {
	if off >= len(b) {
		return nil
	}
	end := min(len(b), off+n)
	return b[off:end]
}
