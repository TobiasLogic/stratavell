package engine

import (
	"context"
	"database/sql"
	"math"
	"runtime"
	"sort"
	"sync"
)

type hist struct {
	n    int64
	sum  float64
	max  float64
	ring []float64
	pos  int
}

type Metrics struct {
	mu       sync.Mutex
	counters map[string]float64
	gauges   map[string]float64
	hists    map[string]*hist
}

func newMetrics() *Metrics {
	return &Metrics{counters: map[string]float64{}, gauges: map[string]float64{}, hists: map[string]*hist{}}
}

func (m *Metrics) inc(name string, v float64) {
	m.mu.Lock()
	m.counters[name] += v
	m.mu.Unlock()
}

func (m *Metrics) set(name string, v float64) {
	m.mu.Lock()
	m.gauges[name] = v
	m.mu.Unlock()
}

func (m *Metrics) observe(name string, v float64) {
	m.mu.Lock()
	h := m.hists[name]
	if h == nil {
		h = &hist{ring: make([]float64, 0, 4096)}
		m.hists[name] = h
	}
	h.n++
	h.sum += v
	h.max = math.Max(h.max, v)
	if len(h.ring) < cap(h.ring) {
		h.ring = append(h.ring, v)
	} else {
		h.ring[h.pos] = v
		h.pos = (h.pos + 1) % len(h.ring)
	}
	m.mu.Unlock()
}

type HistSnapshot struct {
	Count int64   `json:"count"`
	Mean  float64 `json:"mean"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	Max   float64 `json:"max"`
}

type MetricsSnapshot struct {
	Counters   map[string]float64      `json:"counters"`
	Gauges     map[string]float64      `json:"gauges"`
	Histograms map[string]HistSnapshot `json:"histograms"`
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(q*float64(len(sorted)))) - 1
	return sorted[max(0, min(i, len(sorted)-1))]
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := MetricsSnapshot{Counters: map[string]float64{}, Gauges: map[string]float64{}, Histograms: map[string]HistSnapshot{}}
	for k, v := range m.counters {
		s.Counters[k] = v
	}
	for k, v := range m.gauges {
		s.Gauges[k] = v
	}
	for k, h := range m.hists {
		c := append([]float64(nil), h.ring...)
		sort.Float64s(c)
		mean := 0.0
		if h.n > 0 {
			mean = h.sum / float64(h.n)
		}
		s.Histograms[k] = HistSnapshot{Count: h.n, Mean: mean, P50: quantile(c, 0.5), P95: quantile(c, 0.95), P99: quantile(c, 0.99), Max: h.max}
	}
	return s
}

func (m *Metrics) Reset() {
	m.mu.Lock()
	m.counters = map[string]float64{}
	m.gauges = map[string]float64{}
	m.hists = map[string]*hist{}
	m.mu.Unlock()
}

func (e *Engine) flushMetrics(ctx context.Context) error {
	s := e.metrics.Snapshot()
	return e.db.Tx(ctx, func(tx *sql.Tx) error {
		now := e.now()
		for k, v := range s.Counters {
			if _, err := tx.ExecContext(ctx, "INSERT INTO metrics(session_id,agent_id,name,value,updated_at) VALUES('','',?,?,?) ON CONFLICT(session_id,agent_id,name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at", "counter."+k, v, now); err != nil {
				return err
			}
		}
		for k, h := range s.Histograms {
			for name, v := range map[string]float64{"p50": h.P50, "p95": h.P95, "max": h.Max, "count": float64(h.Count)} {
				if _, err := tx.ExecContext(ctx, "INSERT INTO metrics(session_id,agent_id,name,value,updated_at) VALUES('','',?,?,?) ON CONFLICT(session_id,agent_id,name) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at", "hist."+k+"."+name, v, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

type AgentStats struct {
	Session           string  `json:"session"`
	Agent             string  `json:"agent"`
	Generation        int64   `json:"generation"`
	HeadSeq           int64   `json:"head_seq"`
	BoundarySeq       int64   `json:"boundary_seq"`
	MaskSeq           int64   `json:"mask_seq"`
	ActiveTokens      int     `json:"active_tokens_estimate"`
	Capacity          int     `json:"effective_capacity"`
	RawItems          int     `json:"raw_items"`
	FrontierNodes     int     `json:"frontier_nodes"`
	FrontierTokens    int     `json:"frontier_tokens"`
	ArchivedEvents    int64   `json:"archived_events"`
	ArchivedTokens    int64   `json:"archived_tokens"`
	TotalEvents       int64   `json:"total_events"`
	TotalTokens       int64   `json:"total_tokens"`
	CompressionRatio  float64 `json:"compression_ratio"`
	SummaryNodes      int64   `json:"summary_nodes"`
	DAGDepth          int     `json:"dag_depth"`
	PendingJobs       int     `json:"pending_jobs"`
	ReadyResults      int     `json:"ready_results"`
	CalibrationRatio  float64 `json:"calibration_ratio"`
	GrowthPerTurn     float64 `json:"growth_tokens_per_turn"`
	SummarizerLatS    float64 `json:"summarizer_latency_s"`
	SummariesShown    int     `json:"summaries_shown"`
	SummariesExpanded int     `json:"summaries_expanded"`
	SummarySearchHits int     `json:"summary_search_hits"`
}

func (a *Agent) Stats(ctx context.Context) (AgentStats, error) {
	a.mu.Lock()
	st := AgentStats{Session: a.s.id, Agent: a.id, Generation: a.gen, HeadSeq: a.head, BoundarySeq: a.boundary, MaskSeq: a.maskSeq, ActiveTokens: a.lastEst, Capacity: a.capacity(), RawItems: len(a.raw), FrontierNodes: len(a.frontier), FrontierTokens: a.summaryTokens(), PendingJobs: len(a.leafJobs) + boolInt(a.condenseJob != ""), ReadyResults: len(a.ready), CalibrationRatio: a.calib.Ratio(), GrowthPerTurn: a.pred.growth, SummarizerLatS: a.pred.lat}
	for _, n := range a.frontier {
		st.DAGDepth = max(st.DAGDepth, n.Level)
	}
	st.SummariesShown, st.SummariesExpanded, st.SummarySearchHits = len(a.sumShown), len(a.sumExpanded), a.sumHits
	boundary := a.boundary
	a.mu.Unlock()
	q := a.e.db.R
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(tokens),0) FROM events WHERE session_id=? AND agent_id=?", a.s.id, a.id).Scan(&st.TotalEvents, &st.TotalTokens); err != nil {
		return st, err
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(tokens),0) FROM events WHERE session_id=? AND agent_id=? AND seq<=?", a.s.id, a.id, boundary).Scan(&st.ArchivedEvents, &st.ArchivedTokens); err != nil {
		return st, err
	}
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM summaries WHERE session_id=? AND agent_id=?", a.s.id, a.id).Scan(&st.SummaryNodes); err != nil {
		return st, err
	}
	if st.ActiveTokens > 0 {
		st.CompressionRatio = float64(st.TotalTokens) / float64(st.ActiveTokens)
	}
	return st, nil
}

type EngineStats struct {
	Metrics   MetricsSnapshot `json:"metrics"`
	DBBytes   int64           `json:"db_bytes"`
	WALBytes  int64           `json:"wal_bytes"`
	Blobs     int64           `json:"blobs"`
	BlobRaw   int64           `json:"blob_raw_bytes"`
	BlobStore int64           `json:"blob_stored_bytes"`
	Events    int64           `json:"events"`
	EventRaw  int64           `json:"event_raw_bytes"`
	Summaries int64           `json:"summaries"`
	Docs      int64           `json:"index_docs"`
	TxCount   int64           `json:"write_transactions"`
	TxMS      float64         `json:"write_tx_ms_total"`
	HeapBytes uint64          `json:"heap_bytes"`
	Goroutine int             `json:"goroutines"`
}

func (e *Engine) Stats(ctx context.Context) (EngineStats, error) {
	var s EngineStats
	s.Metrics = e.metrics.Snapshot()
	sz, err := e.db.Size(ctx)
	if err != nil {
		return s, err
	}
	s.DBBytes, s.WALBytes = sz.FileBytes, sz.WALBytes
	q := e.db.R
	_ = q.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(size),0), COALESCE(SUM(stored_size),0) FROM blobs").Scan(&s.Blobs, &s.BlobRaw, &s.BlobStore)
	_ = q.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(raw_bytes),0) FROM events").Scan(&s.Events, &s.EventRaw)
	_ = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM summaries").Scan(&s.Summaries)
	_ = q.QueryRowContext(ctx, "SELECT COUNT(*) FROM docs").Scan(&s.Docs)
	n, d := e.db.TxStats()
	s.TxCount, s.TxMS = n, float64(d.Microseconds())/1000
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.HeapBytes = ms.HeapAlloc
	s.Goroutine = runtime.NumGoroutine()
	return s, nil
}

type FrontierNode struct {
	ID     string `json:"id"`
	Level  int    `json:"level"`
	Kind   string `json:"kind"`
	Lo     int64  `json:"seq_lo"`
	Hi     int64  `json:"seq_hi"`
	Tokens int    `json:"tokens"`
	Model  string `json:"model"`
}

func (a *Agent) Frontier() []FrontierNode {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]FrontierNode, len(a.frontier))
	for i, n := range a.frontier {
		out[i] = FrontierNode{ID: n.ID, Level: n.Level, Kind: n.Kind, Lo: n.Lo, Hi: n.Hi, Tokens: n.Tokens, Model: n.Model}
	}
	return out
}

func (s *Session) Agents(ctx context.Context) ([]string, error) {
	rows, err := s.e.db.R.QueryContext(ctx, "SELECT id FROM agents WHERE session_id=? ORDER BY created_at, id", s.id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (e *Engine) Sessions(ctx context.Context) ([]string, error) {
	rows, err := e.db.R.QueryContext(ctx, "SELECT id FROM sessions ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
