package engine

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/TobiasLogic/palimpsest/internal/store"
	"github.com/TobiasLogic/palimpsest/internal/textproc"
	"github.com/TobiasLogic/palimpsest/msg"
)

type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Name() string
}

type indexReq struct {
	event   string
	summary string
}

type indexer struct {
	e      *Engine
	sync   bool
	mu     sync.Mutex
	cond   *sync.Cond
	queue  []indexReq
	busy   bool
	stopped bool
	wg     sync.WaitGroup
}

func newIndexer(e *Engine, syncMode bool) *indexer {
	ix := &indexer{e: e, sync: syncMode}
	ix.cond = sync.NewCond(&ix.mu)
	return ix
}

func sessionToken(id string) string { return "s" + store.HashBytes([]byte(id))[:20] }

func (ix *indexer) start() {
	if !ix.sync {
		ix.wg.Add(1)
		go ix.loop()
	}
	go ix.backfill()
}

func (ix *indexer) stop() {
	ix.mu.Lock()
	ix.stopped = true
	ix.cond.Broadcast()
	ix.mu.Unlock()
	ix.wg.Wait()
}

func (ix *indexer) backfill() {
	rows, err := ix.e.db.R.Query("SELECT id FROM events WHERE indexed=0 ORDER BY ts LIMIT 100000")
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		ix.enqueue(id)
	}
}

func (ix *indexer) enqueue(eventID string) {
	if ix.sync {
		_ = ix.e.indexEvent(context.Background(), eventID)
		return
	}
	ix.mu.Lock()
	ix.queue = append(ix.queue, indexReq{event: eventID})
	ix.cond.Signal()
	ix.mu.Unlock()
}

func (ix *indexer) enqueueSummary(id string) {
	if ix.sync {
		_ = ix.e.indexSummary(context.Background(), id)
		return
	}
	ix.mu.Lock()
	ix.queue = append(ix.queue, indexReq{summary: id})
	ix.cond.Signal()
	ix.mu.Unlock()
}

func (ix *indexer) loop() {
	defer ix.wg.Done()
	for {
		ix.mu.Lock()
		for len(ix.queue) == 0 && !ix.stopped {
			ix.cond.Wait()
		}
		if ix.stopped && len(ix.queue) == 0 {
			ix.mu.Unlock()
			return
		}
		batch := ix.queue
		ix.queue = nil
		ix.busy = true
		ix.mu.Unlock()
		for _, r := range batch {
			if r.event != "" {
				_ = ix.e.indexEvent(context.Background(), r.event)
			} else {
				_ = ix.e.indexSummary(context.Background(), r.summary)
			}
		}
		ix.mu.Lock()
		ix.busy = false
		ix.cond.Broadcast()
		ix.mu.Unlock()
	}
}

func (ix *indexer) wait(timeout time.Duration) {
	if ix.sync {
		return
	}
	deadline := time.Now().Add(timeout)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	for (len(ix.queue) > 0 || ix.busy) && time.Now().Before(deadline) && !ix.stopped {
		ix.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		ix.mu.Lock()
	}
}

func (e *Engine) WaitIndexed(timeout time.Duration) { e.idx.wait(timeout) }

func chunkText(s string, size int) []string {
	if len(s) <= size {
		return []string{s}
	}
	var out []string
	var cur strings.Builder
	for line := range strings.Lines(s) {
		for len(line) > size {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			cut := size
			for cut > 0 && line[cut]&0xC0 == 0x80 {
				cut--
			}
			out = append(out, line[:cut])
			line = line[cut:]
		}
		if cur.Len()+len(line) > size && cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteString(line)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func auxTerms(ev Event, extra string) string {
	var b strings.Builder
	for _, p := range ev.Paths {
		b.WriteString(p)
		b.WriteByte(' ')
		for _, s := range textproc.SplitIdent(p) {
			b.WriteString(s)
			b.WriteByte(' ')
		}
	}
	for _, s := range ev.Symbols {
		for _, w := range textproc.SplitIdent(s) {
			b.WriteString(w)
			b.WriteByte(' ')
		}
	}
	b.WriteString(ev.ToolName)
	b.WriteByte(' ')
	b.WriteString(extra)
	return b.String()
}

func reliability(ev Event) int {
	switch ev.Role {
	case msg.RoleUser:
		return 3
	case msg.RoleTool:
		return 3
	case msg.RoleAssistant:
		return 1
	}
	return 0
}

func (e *Engine) indexEvent(ctx context.Context, id string) error {
	ev, m, sm, err := e.loadEvent(ctx, e.db.R, id)
	if err != nil {
		return err
	}
	if ev.ContentType == msg.CTRetrieval {
		return e.db.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "UPDATE events SET indexed=1 WHERE id=?", id)
			return err
		})
	}
	text := m.PlainText()
	if len(text) > e.pol.Retrieval.IndexMaxBytes {
		head := text[:e.pol.Retrieval.IndexMaxBytes]
		text = head
		if sm.Digest != nil {
			text += "\n" + textproc.FormatDigest(*sm.Digest)
		}
	}
	chunks := chunkText(text, e.pol.Retrieval.ChunkBytes)
	aux := auxTerms(ev, sm.CmdKey)
	sid := sessionToken(ev.SessionID)
	var vecs [][]float32
	if e.embedder != nil {
		ectx, cancel := context.WithTimeout(ctx, 5*time.Second)
		vecs, err = e.embedder.Embed(ectx, chunks)
		cancel()
		if err != nil || len(vecs) != len(chunks) {
			vecs = nil
			e.metrics.inc("embedding_failures", 1)
		}
	}
	return e.db.Tx(ctx, func(tx *sql.Tx) error {
		var done int
		if err := tx.QueryRowContext(ctx, "SELECT indexed FROM events WHERE id=?", id).Scan(&done); err != nil {
			return err
		}
		if done == 1 {
			return nil
		}
		off := 0
		for i, c := range chunks {
			res, err := tx.ExecContext(ctx, "INSERT INTO docs(session_id,agent_id,scope,ref_kind,ref_id,chunk,byte_off,seq,ts,tool,reliability) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
				ev.SessionID, ev.AgentID, ev.Scope, "event", ev.ID, i, off, ev.Seq, ev.TS, ev.ToolName, reliability(ev))
			if err != nil {
				return err
			}
			docID, _ := res.LastInsertId()
			a := ""
			if i == 0 {
				a = aux
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO docs_fts(rowid, body, aux, sid) VALUES(?,?,?,?)", docID, c, a, sid); err != nil {
				return err
			}
			if vecs != nil {
				if _, err := tx.ExecContext(ctx, "INSERT INTO vectors(doc_id,session_id,dim,vec) VALUES(?,?,?,?)", docID, ev.SessionID, len(vecs[i]), encodeVec(vecs[i])); err != nil {
					return err
				}
			}
			off += len(c)
		}
		_, err := tx.ExecContext(ctx, "UPDATE events SET indexed=1 WHERE id=?", id)
		return err
	})
}

func (e *Engine) indexSummary(ctx context.Context, id string) error {
	n, err := e.loadSummary(ctx, e.db.R, id)
	if err != nil {
		return err
	}
	var scope string
	if err := e.db.R.QueryRowContext(ctx, "SELECT CASE WHEN EXISTS(SELECT 1 FROM docs WHERE ref_id=?) THEN 'x' ELSE 'private' END", id).Scan(&scope); err != nil {
		return err
	}
	if scope == "x" {
		return nil
	}
	text := n.Text + factsText(n.Facts)
	sid := sessionToken(n.SessionID)
	return e.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO docs(session_id,agent_id,scope,ref_kind,ref_id,chunk,byte_off,seq,ts,tool,reliability) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
			n.SessionID, n.AgentID, "private", "summary", n.ID, 0, 0, n.Hi, n.CreatedAt, "", 2)
		if err != nil {
			return err
		}
		docID, _ := res.LastInsertId()
		_, err = tx.ExecContext(ctx, "INSERT INTO docs_fts(rowid, body, aux, sid) VALUES(?,?,?,?)", docID, text, n.Kind+" summary level", sid)
		return err
	})
}

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func decodeVec(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
