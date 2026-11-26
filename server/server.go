package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/adapters/anthropic"
	"github.com/TobiasLogic/palimpsest/adapters/openai"
	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
)

const APIVersion = "v1"

var Version = "0.1.0"

type Server struct {
	E       *engine.Engine
	Token   string
	MaxBody int64
	mux     *http.ServeMux
}

func New(e *engine.Engine, token string) *Server {
	s := &Server{E: e, Token: token, MaxBody: 64 << 20, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.Token != "" && r.URL.Path != "/v1/health" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", errors.New("missing or invalid bearer token"))
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBody)
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	m := s.mux
	m.HandleFunc("GET /v1/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	m.HandleFunc("GET /v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"api": APIVersion, "version": Version, "config_version": engine.ConfigVersion})
	})
	m.HandleFunc("GET /v1/stats", s.engineStats)
	m.HandleFunc("POST /v1/sessions", s.createSession)
	m.HandleFunc("DELETE /v1/sessions/{sid}", s.deleteSession)
	m.HandleFunc("GET /v1/sessions/{sid}/verify", s.verify)
	m.HandleFunc("POST /v1/sessions/{sid}/agents", s.createAgent)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/events", s.appendEvents)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/assemble", s.assemble)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/usage", s.usage)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/tools/{name}", s.tool)
	m.HandleFunc("GET /v1/sessions/{sid}/agents/{aid}/search", s.search)
	m.HandleFunc("GET /v1/sessions/{sid}/agents/{aid}/events/{eid}", s.getEvent)
	m.HandleFunc("GET /v1/sessions/{sid}/agents/{aid}/artifacts/{ref}", s.getArtifact)
	m.HandleFunc("GET /v1/sessions/{sid}/agents/{aid}/task-state", s.taskState)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/task-state", s.updateTask)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/constraints", s.addConstraint)
	m.HandleFunc("GET /v1/sessions/{sid}/agents/{aid}/lineage/{id}", s.lineage)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/expand", s.expand)
	m.HandleFunc("GET /v1/sessions/{sid}/agents/{aid}/stats", s.agentStats)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/sync", s.sync)
	m.HandleFunc("POST /v1/sessions/{sid}/agents/{aid}/complete", s.complete)
	m.HandleFunc("GET /v1/tools", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, engine.MemoryTools()) })
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, apiError{Error: err.Error(), Code: code})
}

func classify(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, engine.ErrNotFound), errors.Is(err, engine.ErrUnknownAgent):
		writeErr(w, 404, "not_found", err)
	case errors.Is(err, engine.ErrAccessDenied):
		writeErr(w, 403, "access_denied", err)
	case errors.Is(err, engine.ErrProtectedOverflow):
		writeErr(w, 422, "protected_overflow", err)
	case errors.Is(err, engine.ErrContextOverflow):
		writeErr(w, 422, "context_overflow", err)
	case errors.Is(err, engine.ErrPendingToolCalls):
		writeErr(w, 409, "pending_tool_calls", err)
	case errors.Is(err, engine.ErrVersionConflict):
		writeErr(w, 409, "version_conflict", err)
	case isProtocol(err):
		writeErr(w, 400, "protocol_violation", err)
	default:
		writeErr(w, 500, "internal", err)
	}
}

func isProtocol(err error) bool {
	for _, e := range []error{msg.ErrEmptyMessage, msg.ErrBadRole, msg.ErrToolResultShape, msg.ErrMissingCallID, msg.ErrMisplacedPart, msg.ErrDuplicateCallID, msg.ErrOrphanToolResult, msg.ErrUnansweredCall} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeErr(w, 400, "bad_request", fmt.Errorf("invalid JSON body: %w", err))
		return false
	}
	return true
}

type sessionReq struct {
	ID      string          `json:"id"`
	Project string          `json:"project"`
	Policy  json.RawMessage `json:"policy,omitempty"`
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req sessionReq
	if !decode(w, r, &req) {
		return
	}
	if req.ID == "" {
		writeErr(w, 400, "bad_request", errors.New("id is required"))
		return
	}
	cfg := engine.SessionConfig{Project: req.Project}
	if len(req.Policy) > 0 {
		p, err := engine.ParsePolicy(req.Policy)
		if err != nil {
			writeErr(w, 400, "bad_policy", err)
			return
		}
		cfg.Policy = &p
	}
	sess, err := s.E.OpenSession(r.Context(), req.ID, cfg)
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": sess.ID(), "policy": sess.Policy()})
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	rep, err := s.E.DeleteSession(r.Context(), r.PathValue("sid"))
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) verify(w http.ResponseWriter, r *http.Request) {
	rep, err := s.E.Verify(r.Context(), r.PathValue("sid"))
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, rep)
}

type agentReq struct {
	ID      string          `json:"id"`
	Parent  string          `json:"parent,omitempty"`
	Handoff *engine.Handoff `json:"handoff,omitempty"`
}

func (s *Server) session(r *http.Request) (*engine.Session, error) {
	return s.E.OpenSession(r.Context(), r.PathValue("sid"), engine.SessionConfig{})
}

func (s *Server) agent(r *http.Request) (*engine.Agent, error) {
	sess, err := s.session(r)
	if err != nil {
		return nil, err
	}
	return sess.Agent(r.Context(), r.PathValue("aid"))
}

func (s *Server) createAgent(w http.ResponseWriter, r *http.Request) {
	var req agentReq
	if !decode(w, r, &req) {
		return
	}
	sess, err := s.session(r)
	if err != nil {
		classify(w, err)
		return
	}
	if req.Parent == "" {
		a, err := sess.Agent(r.Context(), req.ID)
		if err != nil {
			classify(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"id": a.ID(), "generation": a.Generation(), "head_seq": a.Head()})
		return
	}
	parent, err := sess.Agent(r.Context(), req.Parent)
	if err != nil {
		classify(w, err)
		return
	}
	h := engine.Handoff{}
	if req.Handoff != nil {
		h = *req.Handoff
	}
	a, err := sess.Spawn(r.Context(), parent, req.ID, h)
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": a.ID(), "parent": parent.ID(), "head_seq": a.Head()})
}

type eventsReq struct {
	Message  *msg.Message  `json:"message,omitempty"`
	Messages []msg.Message `json:"messages,omitempty"`
}

func (s *Server) appendEvents(w http.ResponseWriter, r *http.Request) {
	var req eventsReq
	if !decode(w, r, &req) {
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	list := req.Messages
	if req.Message != nil {
		list = append([]msg.Message{*req.Message}, list...)
	}
	var refs []engine.EventRef
	for _, m := range list {
		ref, err := a.Append(r.Context(), m)
		if err != nil {
			writeJSON(w, statusFor(err), map[string]any{"error": err.Error(), "code": codeFor(err), "appended": refs})
			return
		}
		refs = append(refs, ref)
	}
	writeJSON(w, 200, map[string]any{"events": refs})
}

func statusFor(err error) int {
	if isProtocol(err) {
		return 400
	}
	return 500
}

func codeFor(err error) string {
	if isProtocol(err) {
		return "protocol_violation"
	}
	return "internal"
}

type assembleReq struct {
	System             string          `json:"system"`
	Tools              []msg.ToolDef   `json:"tools"`
	IncludeMemoryTools bool            `json:"include_memory_tools"`
	Query              string          `json:"query"`
	Format             string          `json:"format"`
	Model              string          `json:"model"`
	MaxTokens          int             `json:"max_tokens"`
	CacheTTL           string          `json:"cache_ttl"`
	Thinking           json.RawMessage `json:"thinking,omitempty"`
}

func (s *Server) assemble(w http.ResponseWriter, r *http.Request) {
	var req assembleReq
	if !decode(w, r, &req) {
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	cx, err := a.Assemble(r.Context(), engine.AssembleRequest{System: req.System, Tools: req.Tools, IncludeMemoryTools: req.IncludeMemoryTools, Query: req.Query})
	if err != nil {
		classify(w, err)
		return
	}
	out := map[string]any{"context": cx, "tokens": cx.Tokens, "capacity": cx.Capacity, "generation": cx.Generation, "report": cx.Report}
	switch req.Format {
	case "", "neutral":
	case "anthropic":
		pr, err := anthropic.Render(cx, anthropic.Options{Model: req.Model, MaxTokens: req.MaxTokens, CacheTTL: req.CacheTTL, Thinking: req.Thinking})
		if err != nil {
			writeErr(w, 500, "render_failed", err)
			return
		}
		out["request"] = pr
		delete(out, "context")
	case "openai":
		pr, err := openai.Render(cx, openai.Options{Model: req.Model, MaxTokens: req.MaxTokens})
		if err != nil {
			writeErr(w, 500, "render_failed", err)
			return
		}
		out["request"] = pr
		delete(out, "context")
	default:
		writeErr(w, 400, "bad_request", fmt.Errorf("unknown format %q (neutral, anthropic, openai)", req.Format))
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) usage(w http.ResponseWriter, r *http.Request) {
	var u msg.Usage
	if !decode(w, r, &u) {
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	a.ObserveUsage(u)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) tool(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input json.RawMessage `json:"input"`
	}
	if !decode(w, r, &body) {
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	name := r.PathValue("name")
	if !engine.IsMemoryTool(name) {
		writeErr(w, 404, "not_found", fmt.Errorf("unknown tool %q", name))
		return
	}
	out := a.HandleTool(r.Context(), name, body.Input)
	writeJSON(w, 200, map[string]any{"text": out.Text, "is_error": out.IsError})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := engine.SearchFilters{Path: q.Get("path")}
	if k := q.Get("kinds"); k != "" {
		f.Kinds = strings.Split(k, ",")
	}
	if t := q.Get("tools"); t != "" {
		f.Tools = strings.Split(t, ",")
	}
	f.SeqFrom, _ = strconv.ParseInt(q.Get("seq_from"), 10, 64)
	f.SeqTo, _ = strconv.ParseInt(q.Get("seq_to"), 10, 64)
	hits, err := a.Search(r.Context(), q.Get("q"), f, limit)
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"hits": hits})
}

func (s *Server) getEvent(w http.ResponseWriter, r *http.Request) {
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	ev, m, err := a.GetEvent(r.Context(), r.PathValue("eid"))
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"event": ev, "message": m})
}

func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	n, _ := strconv.Atoi(r.URL.Query().Get("length"))
	data, total, err := a.GetArtifact(r.Context(), r.PathValue("ref"), off, n)
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"offset": off, "length": len(data), "total": total, "data_base64": base64.StdEncoding.EncodeToString(data)})
}

func (s *Server) taskState(w http.ResponseWriter, r *http.Request) {
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	items, err := a.TaskState(r.Context())
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) updateTask(w http.ResponseWriter, r *http.Request) {
	var u engine.TaskUpdate
	if !decode(w, r, &u) {
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	t, err := a.UpdateTask(r.Context(), u)
	if err != nil {
		if errors.Is(err, engine.ErrVersionConflict) {
			classify(w, err)
			return
		}
		writeErr(w, 400, "bad_request", err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) addConstraint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeErr(w, 400, "bad_request", errors.New("text is required"))
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	t, err := a.AddConstraint(r.Context(), body.Text)
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) lineage(w http.ResponseWriter, r *http.Request) {
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	out, err := a.Lineage(r.Context(), r.PathValue("id"))
	if err != nil {
		classify(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(out))
}

func (s *Server) expand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID          string `json:"id"`
		SeqFrom     int64  `json:"seq_from"`
		SeqTo       int64  `json:"seq_to"`
		TokenBudget int    `json:"token_budget"`
	}
	if !decode(w, r, &body) {
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	if body.TokenBudget <= 0 {
		body.TokenBudget = a.Policy().Retrieval.MaxResultTokens
	}
	out, err := a.Expand(r.Context(), body.ID, body.SeqFrom, body.SeqTo, body.TokenBudget)
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"text": out})
}

func (s *Server) agentStats(w http.ResponseWriter, r *http.Request) {
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	st, err := a.Stats(r.Context())
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) engineStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.E.Stats(r.Context())
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) sync(w http.ResponseWriter, r *http.Request) {
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	n, err := a.Sync(r.Context())
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]int{"published": n})
}

func (s *Server) complete(w http.ResponseWriter, r *http.Request) {
	var c engine.Completion
	if !decode(w, r, &c) {
		return
	}
	a, err := s.agent(r)
	if err != nil {
		classify(w, err)
		return
	}
	text, err := a.Complete(r.Context(), c)
	if err != nil {
		classify(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"handoff": text})
}

type Listeners struct {
	Addr   string
	Socket string
}

func (s *Server) Serve(ctx context.Context, l Listeners) error {
	if l.Addr == "" && l.Socket == "" {
		return errors.New("server: no listen address or socket")
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	errs := make(chan error, 2)
	var lns []net.Listener
	if l.Addr != "" {
		ln, err := net.Listen("tcp", l.Addr)
		if err != nil {
			return err
		}
		lns = append(lns, ln)
	}
	if l.Socket != "" {
		_ = os.Remove(l.Socket)
		ln, err := net.Listen("unix", l.Socket)
		if err != nil {
			for _, x := range lns {
				x.Close()
			}
			return err
		}
		if err := os.Chmod(l.Socket, 0o600); err != nil {
			ln.Close()
			return err
		}
		lns = append(lns, ln)
	}
	for _, ln := range lns {
		go func(ln net.Listener) { errs <- srv.Serve(ln) }(ln)
	}
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := srv.Shutdown(sctx)
		if l.Socket != "" {
			_ = os.Remove(l.Socket)
		}
		return err
	case err := <-errs:
		return err
	}
}
