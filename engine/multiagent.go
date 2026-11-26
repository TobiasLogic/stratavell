package engine

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/TobiasLogic/palimpsest/internal/textproc"
	"github.com/TobiasLogic/palimpsest/msg"
)

type Handoff struct {
	Task        string   `json:"task"`
	Constraints []string `json:"constraints,omitempty"`
	Refs        []string `json:"refs,omitempty"`
	Role        string   `json:"role,omitempty"`
	Policy      *Policy  `json:"-"`
}

type Completion struct {
	Summary  string   `json:"summary"`
	Findings []string `json:"findings,omitempty"`
	Refs     []string `json:"refs,omitempty"`
	Failed   bool     `json:"failed,omitempty"`
}

func (s *Session) Spawn(ctx context.Context, parent *Agent, childID string, h Handoff) (*Agent, error) {
	if parent == nil || parent.s != s {
		return nil, fmt.Errorf("%w: parent agent must belong to this session", ErrAccessDenied)
	}
	if childID == "" || childID == parent.id {
		return nil, fmt.Errorf("engine: invalid child agent id %q", childID)
	}
	if existing := s.e.lookupAgent(s.id, childID); existing != nil {
		return nil, fmt.Errorf("engine: agent %s already exists", childID)
	}
	var n int
	if err := s.e.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM agents WHERE session_id=? AND id=?", s.id, childID).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, fmt.Errorf("engine: agent %s already exists", childID)
	}
	vis, err := parent.visibility(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range h.Refs {
		if !parent.canSee(ctx, vis, r) {
			return nil, fmt.Errorf("%w: parent cannot grant %s", ErrAccessDenied, r)
		}
	}
	err = s.e.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO agents(session_id,id,parent_id,role,created_at) VALUES(?,?,?,?,?)", s.id, childID, parent.id, h.Role, s.e.now()); err != nil {
			return err
		}
		for _, r := range h.Refs {
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO grants(session_id,agent_id,ref_id,granted_by,created_at) VALUES(?,?,?,?,?)", s.id, childID, r, parent.id, s.e.now()); err != nil {
				return err
			}
		}
		t := TaskItem{Kind: kindAssignment, Key: "assign:" + childID, Text: fmt.Sprintf("%s assigned to %s: %s", childID, parent.id, textproc.Clip(h.Task, 600)), Status: statusOpen, Provenance: ProvSystem, Scope: "shared", Owner: parent.id, CreatedAt: s.e.now(), ID: s.e.ids.New("tk_"), Version: 1}
		return s.e.insertTaskItemSession(ctx, tx, s.id, t)
	})
	if err != nil {
		return nil, err
	}
	child, err := s.agent(ctx, childID, parent.id, h.Role, h.Policy)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Task handed off from agent %s:\n%s\n", parent.id, h.Task)
	if len(h.Constraints) > 0 {
		b.WriteString("\nConstraints that apply to this task:\n")
		for _, c := range h.Constraints {
			fmt.Fprintf(&b, "- %s\n", c)
		}
	}
	if len(h.Refs) > 0 {
		fmt.Fprintf(&b, "\nRelevant archived items you may read with memory_get_event or memory_expand: %s\n", strings.Join(h.Refs, ", "))
	}
	first := msg.Text(msg.RoleUser, b.String())
	first.Meta = msg.Meta{ContentType: msg.CTHandoff, Protected: true, CauseID: parent.lastEventID()}
	if _, err := child.Append(ctx, first); err != nil {
		return nil, err
	}
	for _, c := range h.Constraints {
		if _, err := child.addConstraintLocked(ctx, c, "private"); err != nil {
			return nil, err
		}
	}
	s.e.metrics.inc("agents_spawned", 1)
	return child, nil
}

func (a *Agent) lastEventID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n := len(a.raw); n > 0 {
		return a.raw[n-1].ev.ID
	}
	return ""
}

func (a *Agent) addConstraintLocked(ctx context.Context, text, scope string) (TaskItem, error) {
	t := TaskItem{Kind: kindConstraint, Key: "constraint:" + store8(text), Text: text, Status: statusOpen, Provenance: ProvUser, Scope: scope, Protected: true}
	var out TaskItem
	err := a.e.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = a.putTask(ctx, tx, t, 0, true)
		return err
	})
	if err != nil {
		return out, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return out, a.refreshPinned(ctx)
}

func (a *Agent) canSee(ctx context.Context, vis *visibility, ref string) bool {
	switch {
	case strings.HasPrefix(ref, "ev_"):
		var session, agent, scope string
		if a.e.db.R.QueryRowContext(ctx, "SELECT session_id, agent_id, scope FROM events WHERE id=?", ref).Scan(&session, &agent, &scope) != nil {
			return false
		}
		return vis.allows(session, agent, scope, ref)
	case strings.HasPrefix(ref, "sm_"):
		var session, agent string
		if a.e.db.R.QueryRowContext(ctx, "SELECT session_id, agent_id FROM summaries WHERE id=?", ref).Scan(&session, &agent) != nil {
			return false
		}
		return vis.allows(session, agent, "private", ref)
	}
	return false
}

func (a *Agent) Complete(ctx context.Context, c Completion) (string, error) {
	status := statusDone
	if c.Failed {
		status = statusFailed
	}
	var refs []string
	vis, err := a.visibility(ctx)
	if err != nil {
		return "", err
	}
	for _, r := range c.Refs {
		if a.canSee(ctx, vis, r) {
			refs = append(refs, r)
		}
	}
	err = a.e.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, f := range c.Findings {
			t := TaskItem{Kind: kindFinding, Key: "finding:" + a.id + ":" + store8(f), Text: f, Status: statusOpen, Provenance: ProvModel, Scope: "shared", Refs: refs}
			if _, err := a.putTask(ctx, tx, t, 0, true); err != nil {
				return err
			}
		}
		t := TaskItem{Kind: kindAssignment, Key: "assign:" + a.id, Text: fmt.Sprintf("%s finished (%s): %s", a.id, status, textproc.Clip(c.Summary, 600)), Status: status, Provenance: ProvSystem, Scope: "shared", Refs: refs}
		if _, err := a.putTask(ctx, tx, t, 0, true); err != nil {
			return err
		}
		if a.parent != "" {
			for _, r := range refs {
				if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO grants(session_id,agent_id,ref_id,granted_by,created_at) VALUES(?,?,?,?,?)", a.s.id, a.parent, r, a.id, a.e.now()); err != nil {
					return err
				}
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE agents SET state=? WHERE session_id=? AND id=?", status, a.s.id, a.id)
		return err
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Agent %s %s.\n%s\n", a.id, status, c.Summary)
	for _, f := range c.Findings {
		fmt.Fprintf(&b, "- finding: %s\n", f)
	}
	if len(refs) > 0 {
		fmt.Fprintf(&b, "evidence: %s\n", strings.Join(refs, ", "))
	}
	return b.String(), nil
}

func (a *Agent) Grant(ctx context.Context, to string, refs []string) error {
	vis, err := a.visibility(ctx)
	if err != nil {
		return err
	}
	for _, r := range refs {
		if !a.canSee(ctx, vis, r) {
			return fmt.Errorf("%w: cannot grant %s", ErrAccessDenied, r)
		}
	}
	return a.e.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, r := range refs {
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO grants(session_id,agent_id,ref_id,granted_by,created_at) VALUES(?,?,?,?,?)", a.s.id, to, r, a.id, a.e.now()); err != nil {
				return err
			}
		}
		return nil
	})
}
