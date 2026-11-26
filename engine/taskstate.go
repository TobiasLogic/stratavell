package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/TobiasLogic/palimpsest/internal/textproc"
	"github.com/TobiasLogic/palimpsest/msg"
)

const (
	kindGoal       = "goal"
	kindConstraint = "constraint"
	kindDirective  = "directive"
	kindPlan       = "plan"
	kindDecision   = "decision"
	kindRejected   = "rejected"
	kindFile       = "file"
	kindSymbol     = "symbol"
	kindTest       = "test"
	kindFailure    = "failure"
	kindAssignment = "assignment"
	kindFinding    = "finding"
	kindNote       = "note"

	statusOpen       = "open"
	statusDone       = "done"
	statusResolved   = "resolved"
	statusModified   = "modified"
	statusPassed     = "passed"
	statusFailed     = "failed"
	statusConflict   = "conflict"
	statusInvalid    = "invalid"
	statusSuperseded = "superseded"

	ProvUser     = "user"
	ProvTool     = "tool"
	ProvModel    = "model"
	ProvInferred = "inferred"
	ProvSystem   = "system"
)

var provRank = map[string]int{ProvSystem: 5, ProvUser: 4, ProvTool: 3, ProvModel: 2, ProvInferred: 1}

var modelKinds = map[string]bool{kindPlan: true, kindDecision: true, kindRejected: true, kindNote: true, kindFinding: true, kindSymbol: true, kindGoal: true}

type TaskItem struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Key         string   `json:"key"`
	Text        string   `json:"text"`
	Status      string   `json:"status"`
	Provenance  string   `json:"provenance"`
	Scope       string   `json:"scope"`
	Owner       string   `json:"owner"`
	SourceEvent string   `json:"source_event,omitempty"`
	Refs        []string `json:"refs,omitempty"`
	Version     int      `json:"version"`
	Supersedes  string   `json:"supersedes,omitempty"`
	Protected   bool     `json:"protected,omitempty"`
	Seq         int64    `json:"seq,omitempty"`
	CreatedAt   int64    `json:"created_at"`
}

type TaskUpdate struct {
	Kind            string   `json:"kind"`
	Key             string   `json:"key"`
	Text            string   `json:"text"`
	Status          string   `json:"status"`
	Refs            []string `json:"refs,omitempty"`
	Shared          bool     `json:"shared,omitempty"`
	ExpectedVersion int      `json:"expected_version,omitempty"`
}

var ErrVersionConflict = errors.New("engine: task item version conflict")

type taskWrite struct {
	item TaskItem
}

func (e *Engine) insertTaskItem(ctx context.Context, tx *sql.Tx, t TaskItem) error {
	refs, _ := json.Marshal(t.Refs)
	_, err := tx.ExecContext(ctx, `INSERT INTO task_items(id,session_id,owner_agent,scope,kind,key,text,status,provenance,source_event,source_refs,version,supersedes,protected,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.ID, "", t.Owner, t.Scope, t.Kind, t.Key, t.Text, t.Status, t.Provenance, nullable(t.SourceEvent), string(refs), t.Version, nullable(t.Supersedes), boolInt(t.Protected), t.CreatedAt)
	return err
}

func (a *Agent) putTask(ctx context.Context, tx *sql.Tx, t TaskItem, expected int, allowConflict bool) (TaskItem, error) {
	if t.Scope == "" {
		t.Scope = "private"
	}
	t.Owner = a.id
	t.CreatedAt = a.e.now()
	t.ID = a.e.ids.New("tk_")
	var curID string
	var curVer int
	var curProv string
	q := "SELECT id, version, provenance FROM task_items WHERE session_id=? AND kind=? AND key=? AND superseded_by IS NULL AND status != 'conflict' AND (owner_agent=? OR scope='shared') ORDER BY version DESC LIMIT 1"
	err := tx.QueryRowContext(ctx, q, a.s.id, t.Kind, t.Key, a.id).Scan(&curID, &curVer, &curProv)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if expected > 0 && !allowConflict {
			return t, fmt.Errorf("%w: %s/%s does not exist", ErrVersionConflict, t.Kind, t.Key)
		}
		t.Version = 1
	case err != nil:
		return t, err
	default:
		if expected > 0 && expected != curVer {
			if !allowConflict {
				return t, fmt.Errorf("%w: %s/%s is at version %d, expected %d", ErrVersionConflict, t.Kind, t.Key, curVer, expected)
			}
			t.Status = statusConflict
			t.Version = curVer
			t.Supersedes = ""
			break
		}
		if provRank[t.Provenance] < provRank[curProv] && t.Kind != kindFailure && t.Kind != kindTest && t.Kind != kindFile {
			t.Status = statusConflict
			t.Version = curVer
			break
		}
		t.Version = curVer + 1
		t.Supersedes = curID
	}
	if err := a.e.insertTaskItemSession(ctx, tx, a.s.id, t); err != nil {
		return t, err
	}
	if t.Supersedes != "" {
		if _, err := tx.ExecContext(ctx, "UPDATE task_items SET superseded_by=? WHERE id=?", t.ID, t.Supersedes); err != nil {
			return t, err
		}
	}
	return t, nil
}

func (e *Engine) insertTaskItemSession(ctx context.Context, tx *sql.Tx, session string, t TaskItem) error {
	refs, _ := json.Marshal(t.Refs)
	_, err := tx.ExecContext(ctx, `INSERT INTO task_items(id,session_id,owner_agent,scope,kind,key,text,status,provenance,source_event,source_refs,version,supersedes,protected,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.ID, session, t.Owner, t.Scope, t.Kind, t.Key, t.Text, t.Status, t.Provenance, nullable(t.SourceEvent), string(refs), t.Version, nullable(t.Supersedes), boolInt(t.Protected), t.CreatedAt)
	return err
}

func (a *Agent) extractTaskState(ctx context.Context, tx *sql.Tx, p *appendPlan) ([]taskWrite, error) {
	ev := p.ev
	var out []taskWrite
	put := func(t TaskItem) error {
		t.SourceEvent = ev.ID
		if len(t.Refs) == 0 {
			t.Refs = []string{ev.ID}
		}
		w, err := a.putTask(ctx, tx, t, 0, true)
		if err != nil {
			return err
		}
		out = append(out, taskWrite{w})
		return nil
	}
	switch ev.Role {
	case msg.RoleUser:
		text := msg.Message{Parts: p.parts}.PlainText()
		if p.meta.Host.ContentType == msg.CTHandoff {
			if err := put(TaskItem{Kind: kindGoal, Key: "handoff:" + ev.ID, Text: textproc.Clip(text, 1200), Status: statusOpen, Provenance: ProvUser, Protected: true}); err != nil {
				return nil, err
			}
		}
		if p.meta.Host.Constraint {
			return out, put(TaskItem{Kind: kindConstraint, Key: "constraint:" + ev.ID, Text: text, Status: statusOpen, Provenance: ProvUser, Scope: "shared", Protected: true})
		}
		if ev.Seq == 1 || a.isFirstUser(ctx, tx) {
			if err := put(TaskItem{Kind: kindGoal, Key: "goal:" + ev.ID, Text: textproc.Clip(text, 1200), Status: statusOpen, Provenance: ProvUser}); err != nil {
				return nil, err
			}
		}
		dt := text
		if a.e.count(text) > a.pol.PinMaxTokens {
			ds := textproc.DirectiveSentences(text, 16)
			dt = fmt.Sprintf("(long message, %d tokens; directive sentences extracted, full text in %s)\n- %s", ev.Tokens, ev.ID, strings.Join(ds, "\n- "))
			if len(ds) == 0 {
				dt = fmt.Sprintf("(long message, %d tokens, full text in %s) %s", ev.Tokens, ev.ID, textproc.Clip(text, 600))
			}
		}
		return out, put(TaskItem{Kind: kindDirective, Key: "directive:" + ev.ID, Text: dt, Status: statusOpen, Provenance: ProvUser, Protected: true})
	case msg.RoleTool:
		switch ev.ContentType {
		case msg.CTFileWrite:
			if ev.IsError {
				return out, nil
			}
			for _, path := range p.primaryList {
				if err := put(TaskItem{Kind: kindFile, Key: path, Text: fmt.Sprintf("%s modified (%s)", path, ev.ID), Status: statusModified, Provenance: ProvTool}); err != nil {
					return nil, err
				}
			}
		case msg.CTTest, msg.CTCommand:
			key := p.meta.CmdKey
			if key == "" {
				key = ev.ToolName
			}
			failed := ev.IsError
			isTest := ev.ContentType == msg.CTTest
			if isTest {
				st := statusPassed
				desc := "passed"
				if failed {
					st = statusFailed
					desc = "failed"
				}
				if d := p.meta.Digest; d != nil {
					desc += "; " + digestLine(d)
				}
				if err := put(TaskItem{Kind: kindTest, Key: key, Text: fmt.Sprintf("%s: %s (%s)", key, desc, ev.ID), Status: st, Provenance: ProvTool}); err != nil {
					return nil, err
				}
			}
			if failed {
				desc := "failed"
				if ev.ExitCode != nil {
					desc = fmt.Sprintf("exit %d", *ev.ExitCode)
				}
				if d := p.meta.Digest; d != nil {
					desc += "; " + digestLine(d)
				}
				return out, put(TaskItem{Kind: kindFailure, Key: key, Text: fmt.Sprintf("%s: %s (%s)", key, desc, ev.ID), Status: statusOpen, Provenance: ProvTool})
			}
			if cur, ok, err := a.currentTask(ctx, tx, kindFailure, key); err != nil {
				return nil, err
			} else if ok && cur.Status == statusOpen {
				return out, put(TaskItem{Kind: kindFailure, Key: key, Text: fmt.Sprintf("%s: resolved by %s (was: %s)", key, ev.ID, textproc.Clip(cur.Text, 200)), Status: statusResolved, Provenance: ProvTool})
			}
		}
	}
	return out, nil
}

func digestLine(d *textproc.LogDigest) string {
	var parts []string
	if len(d.FailedTests) > 0 {
		ft := d.FailedTests
		if len(ft) > 5 {
			ft = append(append([]string(nil), ft[:5]...), fmt.Sprintf("+%d more", len(d.FailedTests)-5))
		}
		parts = append(parts, "failing: "+strings.Join(ft, ", "))
	}
	for i, e := range d.Errors {
		if i >= 2 {
			break
		}
		parts = append(parts, fmt.Sprintf("L%d %s", e.N, textproc.Clip(e.Text, 200)))
	}
	if len(d.Locations) > 0 {
		locs := d.Locations
		if len(locs) > 3 {
			locs = locs[:3]
		}
		parts = append(parts, "at "+strings.Join(locs, ", "))
	}
	if len(parts) == 0 && d.SummaryLine != "" {
		parts = append(parts, d.SummaryLine)
	}
	return strings.Join(parts, "; ")
}

func (a *Agent) isFirstUser(ctx context.Context, tx *sql.Tx) bool {
	var n int
	_ = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE session_id=? AND agent_id=? AND role='user'", a.s.id, a.id).Scan(&n)
	return n <= 1
}

func (a *Agent) currentTask(ctx context.Context, q queryer, kind, key string) (TaskItem, bool, error) {
	row := q.QueryRowContext(ctx, "SELECT "+taskCols+" FROM task_items t LEFT JOIN events e ON e.id=t.source_event WHERE t.session_id=? AND t.kind=? AND t.key=? AND t.superseded_by IS NULL AND t.status != 'conflict' AND (t.owner_agent=? OR t.scope='shared') ORDER BY t.version DESC LIMIT 1", a.s.id, kind, key, a.id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

const taskCols = "t.id,t.kind,t.key,t.text,t.status,t.provenance,t.scope,t.owner_agent,t.source_event,t.source_refs,t.version,t.supersedes,t.protected,t.created_at,COALESCE(e.seq,0)"

func scanTask(r interface{ Scan(...any) error }) (TaskItem, error) {
	var t TaskItem
	var src, sup sql.NullString
	var refs string
	var prot int
	err := r.Scan(&t.ID, &t.Kind, &t.Key, &t.Text, &t.Status, &t.Provenance, &t.Scope, &t.Owner, &src, &refs, &t.Version, &sup, &prot, &t.CreatedAt, &t.Seq)
	if err != nil {
		return t, err
	}
	t.SourceEvent, t.Supersedes, t.Protected = src.String, sup.String, prot == 1
	_ = json.Unmarshal([]byte(refs), &t.Refs)
	return t, nil
}

func (e *Engine) liveTaskItems(ctx context.Context, q queryer, session, agent string, kinds []string) ([]TaskItem, error) {
	query := "SELECT " + taskCols + " FROM task_items t LEFT JOIN events e ON e.id=t.source_event WHERE t.session_id=? AND t.superseded_by IS NULL AND (t.owner_agent=? OR t.scope='shared')"
	args := []any{session, agent}
	if len(kinds) > 0 {
		query += " AND t.kind IN (" + strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",") + ")"
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	query += " ORDER BY t.created_at, t.id"
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TaskItem
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (a *Agent) UpdateTask(ctx context.Context, u TaskUpdate) (TaskItem, error) {
	if !modelKinds[u.Kind] {
		return TaskItem{}, fmt.Errorf("engine: agents may not write task items of kind %q", u.Kind)
	}
	if u.Key == "" {
		u.Key = strings.ToLower(textproc.Clip(u.Text, 60))
	}
	if u.Status == "" {
		u.Status = statusOpen
	}
	t := TaskItem{Kind: u.Kind, Key: u.Key, Text: textproc.Clip(u.Text, 2000), Status: u.Status, Provenance: ProvModel, Refs: u.Refs}
	if u.Shared {
		t.Scope = "shared"
	}
	var out TaskItem
	err := a.e.db.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = a.putTask(ctx, tx, t, u.ExpectedVersion, false)
		return err
	})
	if err == nil {
		a.e.metrics.inc("task_updates", 1)
	}
	return out, err
}

func (a *Agent) AddConstraint(ctx context.Context, text string) (TaskItem, error) {
	t := TaskItem{Kind: kindConstraint, Key: "constraint:" + store8(text), Text: text, Status: statusOpen, Provenance: ProvUser, Scope: "shared", Protected: true}
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

func store8(s string) string {
	h := 0
	for i := 0; i < len(s); i++ {
		h = h*31 + int(s[i])
		h &= 0x7fffffff
	}
	return fmt.Sprintf("%08x", h)
}

func (a *Agent) TaskState(ctx context.Context) ([]TaskItem, error) {
	return a.e.liveTaskItems(ctx, a.e.db.R, a.s.id, a.id, nil)
}

var sectionOrder = []struct {
	kind  string
	title string
}{
	{kindFailure, "Open failures"},
	{kindGoal, "Goals"},
	{kindPlan, "Plan"},
	{kindDecision, "Decisions"},
	{kindRejected, "Rejected approaches"},
	{kindAssignment, "Assignments"},
	{kindFinding, "Findings"},
	{kindTest, "Latest test results"},
	{kindFile, "Modified files"},
	{kindSymbol, "Key symbols"},
	{kindNote, "Notes"},
}

func (a *Agent) renderTaskState(items []TaskItem, budget int) (string, int) {
	by := map[string][]TaskItem{}
	for _, t := range items {
		if t.Kind == kindConstraint || t.Kind == kindDirective {
			continue
		}
		if t.Kind == kindFailure && t.Status != statusOpen {
			continue
		}
		by[t.Kind] = append(by[t.Kind], t)
	}
	var b strings.Builder
	b.WriteString("<task_state note=\"maintained by the context engine; tags show provenance: user, tool (verified observation), model (agent-recorded), inferred (summarizer)\">\n")
	used := a.e.count(b.String()) + 10
	omitted := 0
	for _, sec := range sectionOrder {
		list := by[sec.kind]
		if len(list) == 0 {
			continue
		}
		sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt > list[j].CreatedAt })
		head := sec.title + ":\n"
		ht := a.e.count(head)
		if used+ht > budget {
			omitted += len(list)
			continue
		}
		b.WriteString(head)
		used += ht
		for _, t := range list {
			line := fmt.Sprintf("- [%s %s] %s", t.Provenance, t.Status, textproc.Clip(strings.ReplaceAll(t.Text, "\n", " "), 400))
			if t.Status == statusConflict {
				line += " (CONFLICT: disagrees with another entry for the same key; check provenance)"
			}
			line += "\n"
			lt := a.e.count(line)
			if used+lt > budget {
				omitted++
				continue
			}
			b.WriteString(line)
			used += lt
		}
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d entries omitted for budget; memory_get_task_state returns all)\n", omitted)
	}
	b.WriteString("</task_state>")
	return b.String(), a.e.count(b.String())
}

func (a *Agent) renderTaskDelta(items []TaskItem, since int64, budget int) (string, int) {
	var lines []string
	used := 0
	omitted := 0
	for i := len(items) - 1; i >= 0; i-- {
		t := items[i]
		if t.CreatedAt <= since || t.Kind == kindConstraint || t.Kind == kindDirective {
			continue
		}
		line := fmt.Sprintf("- %s [%s %s] %s", t.Kind, t.Provenance, t.Status, textproc.Clip(strings.ReplaceAll(t.Text, "\n", " "), 300))
		if t.Status == statusConflict {
			line += " (CONFLICT: check provenance)"
		}
		lt := a.e.count(line) + 1
		if used+lt > budget {
			omitted++
			continue
		}
		lines = append(lines, line)
		used += lt
	}
	if len(lines) == 0 {
		return "", 0
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	s := "<task_state_changes note=\"task state updates since the snapshot in the archive block; newest last\">\n" + strings.Join(lines, "\n")
	if omitted > 0 {
		s += fmt.Sprintf("\n(%d older changes omitted; memory_get_task_state returns all)", omitted)
	}
	s += "\n</task_state_changes>"
	return s, a.e.count(s) + 4
}
