package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/TobiasLogic/palimpsest/internal/store"
	"github.com/TobiasLogic/palimpsest/internal/tokens"
	"github.com/TobiasLogic/palimpsest/msg"
)

type Options struct {
	Path       string
	Policy     Policy
	Store      store.Options
	Blob       store.BlobPolicy
	Summarizer Summarizer
	Embedder   Embedder
	Clock      Clock
	IDs        IDSource
	Tokenizer  Tokenizer
	Artifacts  ArtifactStore
	Ranker     Ranker
	Trigger    Trigger
	Manual     bool
	SyncIndex  bool
}

type Engine struct {
	db         *store.DB
	pol        Policy
	blobPol    store.BlobPolicy
	clock      Clock
	ids        IDSource
	summarizer Summarizer
	embedder   Embedder
	sched      *scheduler
	idx        *indexer
	metrics    *Metrics
	manual     bool
	tok        Tokenizer
	artifacts  ArtifactStore
	ranker     Ranker
	trigger    Trigger
	df         dfStats

	mu     sync.Mutex
	agents map[agentKey]*Agent
	sess   map[string]*Session
	closed bool
}

type agentKey struct{ session, agent string }

var (
	ErrClosed            = errors.New("engine: closed")
	ErrUnknownAgent      = errors.New("engine: unknown agent")
	ErrAccessDenied      = errors.New("engine: access denied")
	ErrProtectedOverflow = errors.New("engine: protected content exceeds the available input budget")
	ErrContextOverflow   = errors.New("engine: context cannot be reduced below the input budget")
	ErrNotFound          = errors.New("engine: not found")
)

func Open(ctx context.Context, o Options) (*Engine, error) {
	if o.Path == "" {
		return nil, fmt.Errorf("engine: Options.Path is required")
	}
	if o.Policy.Version == 0 {
		o.Policy = DefaultPolicy()
	}
	if err := o.Policy.Validate(); err != nil {
		return nil, err
	}
	if o.Clock == nil {
		o.Clock = systemClock{}
	}
	if o.IDs == nil {
		o.IDs = newULIDSource(o.Clock)
	}
	if o.Tokenizer == nil {
		o.Tokenizer = DefaultTokenizer()
	}
	if o.Ranker == nil {
		o.Ranker = DefaultRanker()
	}
	if o.Trigger == nil {
		o.Trigger = ThresholdTrigger{}
	}
	if o.Blob.CompressMin == 0 && o.Blob.MaxRatio == 0 {
		o.Blob = store.DefaultBlobPolicy()
	}
	db, err := store.Open(ctx, o.Path, o.Store)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		db:         db,
		pol:        o.Policy,
		blobPol:    o.Blob,
		clock:      o.Clock,
		ids:        o.IDs,
		summarizer: o.Summarizer,
		embedder:   o.Embedder,
		metrics:    newMetrics(),
		manual:     o.Manual,
		tok:        o.Tokenizer,
		artifacts:  o.Artifacts,
		ranker:     o.Ranker,
		trigger:    o.Trigger,
		agents:     map[agentKey]*Agent{},
		sess:       map[string]*Session{},
	}
	e.idx = newIndexer(e, o.SyncIndex || o.Manual)
	e.sched = newScheduler(e, o.Policy.Compaction, o.Manual)
	if err := e.recover(ctx); err != nil {
		db.Close()
		return nil, err
	}
	e.idx.start()
	e.sched.start()
	return e, nil
}

func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()
	e.sched.stop()
	e.idx.stop()
	_ = e.flushMetrics(context.Background())
	return e.db.Close()
}

func (e *Engine) count(s string) int {
	if s == "" {
		return 0
	}
	return e.tok.Count(s)
}

func (e *Engine) DB() *store.DB     { return e.db }
func (e *Engine) Policy() Policy    { return e.pol }
func (e *Engine) Metrics() *Metrics { return e.metrics }
func (e *Engine) now() int64        { return e.clock.Now().UnixNano() }
func (e *Engine) Clock() Clock      { return e.clock }
func (e *Engine) SetSummarizer(s Summarizer) {
	e.mu.Lock()
	e.summarizer = s
	e.mu.Unlock()
}

func (e *Engine) getSummarizer() Summarizer {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.summarizer
}

func (e *Engine) recover(ctx context.Context) error {
	_, err := e.db.W.ExecContext(ctx, "UPDATE jobs SET state='queued', attempts=attempts+1 WHERE state='running'")
	if err != nil {
		return err
	}
	_, err = e.db.W.ExecContext(ctx, "UPDATE jobs SET state='abandoned', error='exceeded attempts during recovery' WHERE state='queued' AND attempts >= ?", e.pol.Compaction.MaxAttempts)
	return err
}

type SessionConfig struct {
	Project string
	Policy  *Policy
}

type Session struct {
	e       *Engine
	id      string
	project string
	pol     Policy
}

func (e *Engine) OpenSession(ctx context.Context, id string, cfg SessionConfig) (*Session, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, ErrClosed
	}
	if s, ok := e.sess[id]; ok {
		e.mu.Unlock()
		return s, nil
	}
	e.mu.Unlock()
	pol := e.pol
	if cfg.Policy != nil {
		pol = *cfg.Policy
		if err := pol.Validate(); err != nil {
			return nil, err
		}
	}
	var stored string
	err := e.db.R.QueryRowContext(ctx, "SELECT config FROM sessions WHERE id=?", id).Scan(&stored)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		raw, _ := json.Marshal(pol)
		err = e.db.Tx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO sessions(id,project,created_at,config,config_version) VALUES(?,?,?,?,?)", id, cfg.Project, e.now(), string(raw), ConfigVersion)
			return err
		})
		if err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if cfg.Policy == nil {
			p, perr := ParsePolicy([]byte(stored))
			if perr != nil {
				return nil, fmt.Errorf("engine: session %s config: %w", id, perr)
			}
			pol = p
		}
	}
	var project string
	_ = e.db.R.QueryRowContext(ctx, "SELECT project FROM sessions WHERE id=?", id).Scan(&project)
	s := &Session{e: e, id: id, project: project, pol: pol}
	e.mu.Lock()
	if prev, ok := e.sess[id]; ok {
		e.mu.Unlock()
		return prev, nil
	}
	e.sess[id] = s
	e.mu.Unlock()
	return s, nil
}

func (s *Session) ID() string      { return s.id }
func (s *Session) Policy() Policy  { return s.pol }
func (s *Session) Engine() *Engine { return s.e }

func (s *Session) Agent(ctx context.Context, id string) (*Agent, error) {
	return s.agent(ctx, id, "", "", nil)
}

func (s *Session) AgentWithPolicy(ctx context.Context, id string, pol Policy) (*Agent, error) {
	if err := pol.Validate(); err != nil {
		return nil, err
	}
	return s.agent(ctx, id, "", "", &pol)
}

func (s *Session) agent(ctx context.Context, id, parent, role string, pol *Policy) (*Agent, error) {
	e := s.e
	k := agentKey{s.id, id}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, ErrClosed
	}
	if a, ok := e.agents[k]; ok {
		e.mu.Unlock()
		return a, nil
	}
	e.mu.Unlock()
	err := e.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO agents(session_id,id,parent_id,role,created_at) VALUES(?,?,?,?,?)", s.id, id, nullable(parent), role, e.now())
		return err
	})
	if err != nil {
		return nil, err
	}
	p := s.pol
	if pol != nil {
		p = *pol
	}
	a := newAgent(s, id, p)
	if err := a.load(ctx); err != nil {
		return nil, err
	}
	e.mu.Lock()
	if prev, ok := e.agents[k]; ok {
		e.mu.Unlock()
		return prev, nil
	}
	e.agents[k] = a
	e.mu.Unlock()
	e.sched.requeueRecovered(ctx, a)
	return a, nil
}

func (e *Engine) lookupAgent(session, agent string) *Agent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.agents[agentKey{session, agent}]
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type openCall struct {
	eventID string
	tool    string
	input   json.RawMessage
}

type readInfo struct {
	seq      int64
	eventID  string
	fileHash string
}

type Agent struct {
	e      *Engine
	s      *Session
	id     string
	parent string
	pol    Policy
	calib  *tokens.Calibrator

	mu        sync.Mutex
	head      int64
	gen       int64
	frontier  []*SummaryNode
	boundary  int64
	maskSeq   int64
	raw       []*item
	pairing   *msg.PairingState
	calls     map[string]openCall
	lastRead  map[string]readInfo
	lastWrite map[string]int64
	pinned    []pinItem
	ready     map[string]*readyResult
	pred      predictor
	turn      int64
	retr      retrievalLog
	lastBreak string
	lastUsage msg.Usage
	lastEst   int

	leafJobs      map[string]jobSpec
	condenseJob   string
	failedJobs    []jobSpec
	failCount     map[[2]int64]int
	evidence      evidenceBlock
	waitMS        float64
	squeeze       bool
	emergencyMask int64
	taskSnap      string
	taskSnapTok   int
	taskSnapAt    int64
	pinnedInit    bool
}

func newAgent(s *Session, id string, pol Policy) *Agent {
	return &Agent{
		e:         s.e,
		s:         s,
		id:        id,
		pol:       pol,
		calib:     tokens.NewCalibrator(),
		pairing:   msg.NewPairingState(),
		calls:     map[string]openCall{},
		lastRead:  map[string]readInfo{},
		lastWrite: map[string]int64{},
		ready:     map[string]*readyResult{},
		leafJobs:  map[string]jobSpec{},
		failCount: map[[2]int64]int{},
		retr:      retrievalLog{seen: map[string]retrievalSeen{}},
	}
}

func (a *Agent) ID() string        { return a.id }
func (a *Agent) SessionID() string { return a.s.id }
func (a *Agent) Session() *Session { return a.s }
func (a *Agent) Policy() Policy    { return a.pol }
func (a *Agent) Parent() string    { return a.parent }

func (a *Agent) Generation() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gen
}

func (a *Agent) Head() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.head
}

func since(t time.Time) float64 { return float64(time.Since(t).Microseconds()) / 1000 }
