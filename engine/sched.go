package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

const (
	jobLeaf     = "leaf"
	jobCondense = "condense"
)

type jobSpec struct {
	ID       string
	Session  string
	Agent    string
	Kind     string
	Level    int
	BaseGen  int64
	Lo, Hi   int64
	Sources  []string
	Input    SummarizeInput
	SrcTok   int
	Attempts int
	Created  time.Time
	notBefore time.Time
}

func (j jobSpec) key() string {
	return fmt.Sprintf("%s/%s/%s/%d/%d/%d", j.Session, j.Agent, j.Kind, j.Level, j.Lo, j.Hi)
}

var ErrQueueFull = errors.New("engine: compaction queue is full")

type scheduler struct {
	e      *Engine
	cfg    Compaction
	manual bool

	mu       sync.Mutex
	cond     *sync.Cond
	queues   map[agentKey][]*jobSpec
	order    []agentKey
	rr       int
	inflight map[agentKey]int
	keys     map[string]bool
	queued   int
	running  int
	stopped  bool
	wg       sync.WaitGroup
	cancel   context.CancelFunc
	ctx      context.Context

	gate *rateGate
}

func newScheduler(e *Engine, cfg Compaction, manual bool) *scheduler {
	s := &scheduler{
		e:        e,
		cfg:      cfg,
		manual:   manual,
		queues:   map[agentKey][]*jobSpec{},
		inflight: map[agentKey]int{},
		keys:     map[string]bool{},
		gate:     newRateGate(cfg.RatePerMinute, cfg.TokensPerMinute, cfg.ForegroundReserve, e.clock),
	}
	s.cond = sync.NewCond(&s.mu)
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s
}

func (s *scheduler) start() {
	if s.manual {
		return
	}
	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
}

func (s *scheduler) stop() {
	s.mu.Lock()
	s.stopped = true
	s.cond.Broadcast()
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
}

func (s *scheduler) enqueue(j *jobSpec) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return ErrClosed
	}
	k := j.key()
	if s.keys[k] {
		return nil
	}
	if s.queued >= s.cfg.MaxQueued {
		s.e.metrics.inc("queue_rejections", 1)
		return ErrQueueFull
	}
	ak := agentKey{j.Session, j.Agent}
	if _, ok := s.queues[ak]; !ok {
		s.order = append(s.order, ak)
	}
	s.queues[ak] = append(s.queues[ak], j)
	s.keys[k] = true
	s.queued++
	s.e.metrics.set("queue_depth", float64(s.queued))
	s.cond.Signal()
	return nil
}

func (s *scheduler) next() *jobSpec {
	now := s.e.clock.Now()
	n := len(s.order)
	for i := 0; i < n; i++ {
		idx := (s.rr + i) % n
		ak := s.order[idx]
		q := s.queues[ak]
		if len(q) == 0 || s.inflight[ak] >= s.cfg.MaxInflightAgent {
			continue
		}
		pick := -1
		for qi, j := range q {
			if !j.notBefore.After(now) {
				pick = qi
				break
			}
		}
		if pick < 0 {
			continue
		}
		j := q[pick]
		s.queues[ak] = append(q[:pick:pick], q[pick+1:]...)
		s.inflight[ak]++
		s.queued--
		s.running++
		s.rr = (idx + 1) % n
		s.e.metrics.set("queue_depth", float64(s.queued))
		s.e.metrics.set("workers_busy", float64(s.running))
		return j
	}
	return nil
}

func (s *scheduler) done(j *jobSpec, requeue bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ak := agentKey{j.Session, j.Agent}
	s.inflight[ak]--
	s.running--
	if requeue && !s.stopped {
		s.queues[ak] = append(s.queues[ak], j)
		s.queued++
	} else {
		delete(s.keys, j.key())
	}
	s.e.metrics.set("workers_busy", float64(s.running))
	s.cond.Broadcast()
}

func (s *scheduler) worker() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		var j *jobSpec
		for {
			if s.stopped {
				s.mu.Unlock()
				return
			}
			j = s.next()
			if j != nil {
				break
			}
			if s.queued > 0 {
				s.mu.Unlock()
				select {
				case <-s.ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
				s.mu.Lock()
				continue
			}
			s.cond.Wait()
		}
		s.mu.Unlock()
		requeue := s.run(s.ctx, j)
		s.done(j, requeue)
	}
}

func (s *scheduler) inflightFor(session, agent string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ak := agentKey{session, agent}
	return s.inflight[ak] + len(s.queues[ak])
}

func (s *scheduler) pendingFor(session, agent string) []*jobSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*jobSpec(nil), s.queues[agentKey{session, agent}]...)
}

func (s *scheduler) RunQueued(ctx context.Context) int {
	n := 0
	for {
		s.mu.Lock()
		j := s.next()
		s.mu.Unlock()
		if j == nil {
			return n
		}
		requeue := s.run(ctx, j)
		s.done(j, requeue)
		n++
		if requeue {
			return n
		}
	}
}

func (s *scheduler) RunOne(ctx context.Context, id string) bool {
	s.mu.Lock()
	var j *jobSpec
	for ak, q := range s.queues {
		for i, x := range q {
			if x.ID == id && s.inflight[ak] < s.cfg.MaxInflightAgent {
				j = x
				s.queues[ak] = append(q[:i:i], q[i+1:]...)
				s.inflight[ak]++
				s.queued--
				s.running++
				break
			}
		}
		if j != nil {
			break
		}
	}
	s.mu.Unlock()
	if j == nil {
		return false
	}
	requeue := s.run(ctx, j)
	s.done(j, requeue)
	return true
}

func (s *scheduler) run(parent context.Context, j *jobSpec) bool {
	e := s.e
	summ := e.getSummarizer()
	if summ == nil {
		e.failJob(j, "no summarizer configured", false)
		return false
	}
	if !s.gate.acquireBackground(j.SrcTok) {
		j.notBefore = e.clock.Now().Add(s.gate.backoff())
		e.metrics.inc("rate_deferrals", 1)
		return true
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(parent, s.cfg.JobTimeout)
	defer cancel()
	_ = e.markJob(ctx, j.ID, "running", "")
	out, err := summ.Summarize(ctx, j.Input)
	lat := time.Since(start)
	e.metrics.observe("summarize_ms", float64(lat.Microseconds())/1000)
	if err == nil {
		e.metrics.inc("summarizer_input_tokens", float64(out.InputTokens))
		e.metrics.inc("summarizer_output_tokens", float64(out.OutputTokens))
	}
	if a := e.lookupAgent(j.Session, j.Agent); a != nil {
		a.mu.Lock()
		a.pred.observeLatency(lat.Seconds())
		a.mu.Unlock()
	}
	if err != nil {
		var rl *RateLimitError
		if errors.As(err, &rl) {
			s.gate.penalize(time.Duration(rl.RetryAfterMS) * time.Millisecond)
		}
		j.Attempts++
		if j.Attempts < s.cfg.MaxAttempts && parent.Err() == nil {
			back := s.cfg.RetryBackoff * time.Duration(1<<uint(j.Attempts-1))
			back += time.Duration(rand.Int64N(int64(back/4 + 1)))
			j.notBefore = e.clock.Now().Add(back)
			_ = e.markJob(context.Background(), j.ID, "queued", err.Error())
			e.metrics.inc("job_retries", 1)
			return true
		}
		e.failJob(j, err.Error(), true)
		return false
	}
	node, verr := e.buildSemanticNode(j, out)
	if verr != nil && !j.Input.Strict && j.Attempts+1 < s.cfg.MaxAttempts {
		j.Attempts++
		j.Input.Strict = true
		j.Input.MaxTokens = j.Input.MaxTokens / 2
		_ = e.markJob(context.Background(), j.ID, "queued", verr.Error())
		e.metrics.inc("summary_rejections", 1)
		return true
	}
	if verr != nil {
		e.metrics.inc("summary_rejections", 1)
		node = e.buildPointerNode(j, "semantic summary rejected: "+verr.Error())
	}
	if err := e.completeJob(context.Background(), j, node); err != nil {
		e.failJob(j, err.Error(), true)
		return false
	}
	return false
}

func (e *Engine) markJob(ctx context.Context, id, state, errText string) error {
	return e.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE jobs SET state=?, error=?, attempts=attempts+CASE WHEN ?='queued' THEN 1 ELSE 0 END, started_at=CASE WHEN ?='running' THEN ? ELSE started_at END WHERE id=?", state, errText, state, state, e.now(), id)
		return err
	})
}

func (e *Engine) persistJob(ctx context.Context, j *jobSpec) error {
	src, _ := json.Marshal(j.Sources)
	return e.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO jobs(id,session_id,agent_id,kind,level,base_generation,seq_lo,seq_hi,sources,state,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
			j.ID, j.Session, j.Agent, j.Kind, j.Level, j.BaseGen, j.Lo, j.Hi, string(src), "queued", e.now())
		return err
	})
}

func (e *Engine) failJob(j *jobSpec, reason string, notify bool) {
	e.metrics.inc("job_failures", 1)
	_ = e.db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), "UPDATE jobs SET state='failed', error=?, finished_at=? WHERE id=?", reason, e.now(), j.ID)
		return err
	})
	if a := e.lookupAgent(j.Session, j.Agent); a != nil {
		a.mu.Lock()
		delete(a.leafJobs, j.ID)
		if a.condenseJob == j.ID {
			a.condenseJob = ""
		}
		if notify {
			a.failedJobs = append(a.failedJobs, *j)
		}
		a.mu.Unlock()
	}
}

func (e *Engine) completeJob(ctx context.Context, j *jobSpec, n *SummaryNode) error {
	err := e.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := e.insertSummary(ctx, tx, n); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE jobs SET state='done', summary_id=?, finished_at=? WHERE id=?", n.ID, e.now(), j.ID)
		return err
	})
	if err != nil {
		return err
	}
	e.idx.enqueueSummary(n.ID)
	if a := e.lookupAgent(j.Session, j.Agent); a != nil {
		a.mu.Lock()
		delete(a.leafJobs, j.ID)
		if a.condenseJob == j.ID {
			a.condenseJob = ""
		}
		a.ready[j.ID] = &readyResult{job: *j, node: n}
		a.mu.Unlock()
	}
	return nil
}

func (s *scheduler) requeueRecovered(ctx context.Context, a *Agent) {
	rows, err := s.e.db.R.QueryContext(ctx, "SELECT id, kind, level, base_generation, seq_lo, seq_hi, sources, attempts FROM jobs WHERE session_id=? AND agent_id=? AND state='queued' ORDER BY seq_lo", a.s.id, a.id)
	if err != nil {
		return
	}
	var specs []jobSpec
	for rows.Next() {
		var j jobSpec
		var src string
		if rows.Scan(&j.ID, &j.Kind, &j.Level, &j.BaseGen, &j.Lo, &j.Hi, &src, &j.Attempts) == nil {
			_ = json.Unmarshal([]byte(src), &j.Sources)
			specs = append(specs, j)
		}
	}
	rows.Close()
	var abandoned []string
	a.mu.Lock()
	for _, old := range specs {
		j := a.rebuildJob(old)
		if j == nil {
			abandoned = append(abandoned, old.ID)
			continue
		}
		if err := s.enqueue(j); err != nil {
			abandoned = append(abandoned, old.ID)
			continue
		}
		if j.Kind == jobLeaf {
			a.leafJobs[j.ID] = *j
		} else {
			a.condenseJob = j.ID
		}
		s.e.metrics.inc("jobs_recovered", 1)
	}
	a.mu.Unlock()
	if len(abandoned) == 0 {
		return
	}
	_ = s.e.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, id := range abandoned {
			if _, err := tx.ExecContext(ctx, "UPDATE jobs SET state='abandoned', error='not rebuildable after restart' WHERE id=?", id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *Agent) rebuildJob(old jobSpec) *jobSpec {
	if old.Kind == jobCondense {
		i, k := findSources(a.frontier, old.Sources)
		if i < 0 {
			return nil
		}
		j := a.condenseSpec(a.frontier[i : k+1])
		j.ID, j.Attempts = old.ID, old.Attempts
		return j
	}
	lo, hi := -1, -1
	for idx, it := range a.raw {
		if it.ev.Seq == old.Lo {
			lo = idx
		}
		if it.ev.Seq == old.Hi {
			hi = idx
		}
	}
	if lo < 0 || hi < lo || hi-lo+1 != len(old.Sources) {
		return nil
	}
	tok := 0
	for i := lo; i <= hi; i++ {
		tok += a.e.count(a.summarizerText(a.raw[i])) + 8
	}
	j := a.leafSpec(chunk{lo: lo, hi: hi, tok: tok})
	j.ID, j.Attempts = old.ID, old.Attempts
	return j
}

type rateGate struct {
	mu        sync.Mutex
	clock     Clock
	rpm       int
	tpm       int
	reserve   float64
	reqTimes  []time.Time
	tokTimes  []tokStamp
	fgTokens  []tokStamp
	penalty   time.Time
	backoffN  int
}

type tokStamp struct {
	t time.Time
	n int
}

func newRateGate(rpm, tpm int, reserve float64, clock Clock) *rateGate {
	return &rateGate{rpm: rpm, tpm: tpm, reserve: reserve, clock: clock}
}

func (g *rateGate) trim(now time.Time) {
	cut := now.Add(-time.Minute)
	for len(g.reqTimes) > 0 && g.reqTimes[0].Before(cut) {
		g.reqTimes = g.reqTimes[1:]
	}
	for len(g.tokTimes) > 0 && g.tokTimes[0].t.Before(cut) {
		g.tokTimes = g.tokTimes[1:]
	}
	for len(g.fgTokens) > 0 && g.fgTokens[0].t.Before(cut) {
		g.fgTokens = g.fgTokens[1:]
	}
}

func sumTok(xs []tokStamp) int {
	n := 0
	for _, x := range xs {
		n += x.n
	}
	return n
}

func (g *rateGate) acquireBackground(tokens int) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	if now.Before(g.penalty) {
		return false
	}
	g.trim(now)
	if g.rpm > 0 && len(g.reqTimes) >= g.rpm {
		return false
	}
	if g.tpm > 0 {
		limit := int(float64(g.tpm) * (1 - g.reserve))
		used := sumTok(g.tokTimes) + sumTok(g.fgTokens)
		if used+tokens > limit && len(g.tokTimes) > 0 {
			return false
		}
	}
	g.reqTimes = append(g.reqTimes, now)
	g.tokTimes = append(g.tokTimes, tokStamp{now, tokens})
	g.backoffN = 0
	return true
}

func (g *rateGate) foreground(tokens int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	g.trim(now)
	g.fgTokens = append(g.fgTokens, tokStamp{now, tokens})
}

func (g *rateGate) penalize(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if d <= 0 {
		g.backoffN++
		d = time.Duration(1<<uint(min(g.backoffN, 6))) * time.Second
	}
	g.penalty = g.clock.Now().Add(d)
}

func (g *rateGate) backoff() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.clock.Now()
	if now.Before(g.penalty) {
		return g.penalty.Sub(now)
	}
	return 2 * time.Second
}

func (e *Engine) RunJobs(ctx context.Context) int { return e.sched.RunQueued(ctx) }

func (e *Engine) RunJob(ctx context.Context, id string) bool { return e.sched.RunOne(ctx, id) }

type JobInfo struct {
	ID       string    `json:"id"`
	Session  string    `json:"session"`
	Agent    string    `json:"agent"`
	Kind     string    `json:"kind"`
	Level    int       `json:"level"`
	Lo       int64     `json:"seq_lo"`
	Hi       int64     `json:"seq_hi"`
	SrcTok   int       `json:"source_tokens"`
	Created  time.Time `json:"created"`
	Attempts int       `json:"attempts"`
}

func (e *Engine) PendingJobs(session, agent string) []JobInfo {
	var out []JobInfo
	for _, j := range e.sched.pendingFor(session, agent) {
		out = append(out, JobInfo{ID: j.ID, Session: j.Session, Agent: j.Agent, Kind: j.Kind, Level: j.Level, Lo: j.Lo, Hi: j.Hi, SrcTok: j.SrcTok, Created: j.Created, Attempts: j.Attempts})
	}
	return out
}
