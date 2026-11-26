package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/internal/store"
	"github.com/TobiasLogic/palimpsest/internal/textproc"
	"github.com/TobiasLogic/palimpsest/internal/tokens"
	"github.com/TobiasLogic/palimpsest/msg"
)

func canonicalHash(m msg.Message) (string, []byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", nil, err
	}
	return store.HashBytes(b), b, nil
}

func (e *Engine) messageTokens(m msg.Message) int {
	n := tokens.MessageOverhead
	for _, p := range m.Parts {
		n += e.count(p.Text) + e.count(string(p.Input)) + len(p.ToolName)/4
		if p.Type == msg.PartToolCall || p.Type == msg.PartToolResult {
			n += 8
		}
	}
	return n
}

func inferContentType(m msg.Message, call openCall) msg.ContentType {
	if m.Meta.ContentType != "" {
		return m.Meta.ContentType
	}
	switch m.Role {
	case msg.RoleUser, msg.RoleAssistant, msg.RoleSystem:
		return msg.CTText
	}
	name := strings.ToLower(call.tool)
	if p, ok := m.ToolResultPart(); ok && p.ToolName != "" {
		name = strings.ToLower(p.ToolName)
	}
	switch {
	case strings.HasPrefix(name, "memory_"):
		return msg.CTRetrieval
	case strings.Contains(name, "read") || strings.Contains(name, "view") || name == "cat" || strings.Contains(name, "open_file"):
		return msg.CTFileRead
	case strings.Contains(name, "write") || strings.Contains(name, "edit") || strings.Contains(name, "patch") || strings.Contains(name, "replace"):
		return msg.CTFileWrite
	case strings.Contains(name, "grep") || strings.Contains(name, "search") || strings.Contains(name, "find") || strings.Contains(name, "glob") || name == "ls":
		return msg.CTSearch
	case strings.Contains(name, "test"):
		return msg.CTTest
	case strings.Contains(name, "bash") || strings.Contains(name, "shell") || strings.Contains(name, "exec") || strings.Contains(name, "run") || strings.Contains(name, "command"):
		cmd := commandFromInput(call.input)
		if textproc.IsTestCommand(cmd) {
			return msg.CTTest
		}
		return msg.CTCommand
	}
	return msg.CTText
}

func commandFromInput(in json.RawMessage) string {
	if len(in) == 0 {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(in, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "cmd", "script", "args"} {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				return t
			case []any:
				var parts []string
				for _, x := range t {
					parts = append(parts, fmt.Sprint(x))
				}
				return strings.Join(parts, " ")
			}
		}
	}
	return ""
}

func pathsFromInput(in json.RawMessage) []string {
	if len(in) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(in, &m) != nil {
		return nil
	}
	var out []string
	for _, k := range []string{"path", "file_path", "file", "filename", "target_file"} {
		if v, ok := m[k].(string); ok && v != "" {
			out = append(out, strings.TrimPrefix(v, "./"))
		}
	}
	if v, ok := m["paths"].([]any); ok {
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, strings.TrimPrefix(s, "./"))
			}
		}
	}
	return out
}

type appendPlan struct {
	ev       Event
	body     []storedPart
	blobs    map[string][]byte
	meta     storedMeta
	parts    []msg.Part
	call        openCall
	primary     map[string]bool
	primaryList []string
}

func (a *Agent) Append(ctx context.Context, m msg.Message) (EventRef, error) {
	start := time.Now()
	if err := m.Validate(); err != nil {
		a.e.metrics.inc("protocol_rejections", 1)
		return EventRef{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.pairing.Clone().Step(m); err != nil {
		a.e.metrics.inc("protocol_rejections", 1)
		return EventRef{}, err
	}
	plan, err := a.planAppend(m)
	if err != nil {
		return EventRef{}, err
	}
	var items []taskWrite
	err = a.e.db.Tx(ctx, func(tx *sql.Tx) error {
		if err := a.writeEvent(ctx, tx, plan); err != nil {
			return err
		}
		if a.pol.TaskState {
			w, err := a.extractTaskState(ctx, tx, plan)
			if err != nil {
				return err
			}
			items = w
		}
		_, err := tx.ExecContext(ctx, "UPDATE agents SET head_seq=? WHERE session_id=? AND id=?", plan.ev.Seq, a.s.id, a.id)
		return err
	})
	if err != nil {
		return EventRef{}, err
	}
	_ = a.pairing.Step(m)
	a.head = plan.ev.Seq
	it := &item{ev: plan.ev, parts: plan.parts, meta: plan.meta, primary: plan.primaryList, escape: m.Role == msg.RoleTool && needsEscape(plan.parts)}
	switch m.Role {
	case msg.RoleAssistant:
		for _, p := range m.ToolCalls() {
			a.calls[p.ToolCallID] = openCall{eventID: plan.ev.ID, tool: p.ToolName, input: p.Input}
			it.callIDs = append(it.callIDs, p.ToolCallID)
		}
	case msg.RoleTool:
		p, _ := m.ToolResultPart()
		delete(a.calls, p.ToolCallID)
	}
	a.trackFiles(it)
	a.raw = append(a.raw, it)
	a.pred.observeAppend(plan.ev.Tokens)
	a.e.idx.enqueue(plan.ev.ID)
	a.e.metrics.observe("append_ms", since(start))
	a.e.metrics.inc("events", 1)
	a.e.metrics.inc("raw_tokens", float64(plan.ev.Tokens))
	_ = items
	a.maybeSchedule(ctx, "append")
	return EventRef{ID: plan.ev.ID, Seq: plan.ev.Seq, Tokens: plan.ev.Tokens}, nil
}

func (a *Agent) planAppend(m msg.Message) (*appendPlan, error) {
	hash, _, err := canonicalHash(m)
	if err != nil {
		return nil, err
	}
	p := &appendPlan{blobs: map[string][]byte{}}
	ev := Event{
		ID:          a.e.ids.New("ev_"),
		SessionID:   a.s.id,
		AgentID:     a.id,
		Seq:         a.head + 1,
		TS:          a.e.now(),
		Role:        m.Role,
		ContentHash: hash,
		Tokens:      a.e.messageTokens(m),
		Scope:       "private",
		Protected:   m.Meta.Protected || m.Meta.Constraint,
		CauseID:     m.Meta.CauseID,
	}
	if m.Meta.Shared {
		ev.Scope = "shared"
	}
	if m.Role == msg.RoleTool {
		tr, _ := m.ToolResultPart()
		p.call = a.calls[tr.ToolCallID]
		ev.ToolCallID = tr.ToolCallID
		ev.ToolName = tr.ToolName
		if ev.ToolName == "" {
			ev.ToolName = p.call.tool
		}
		ev.ParentID = p.call.eventID
		ev.GroupID = p.call.eventID
		ev.IsError = tr.IsError
	}
	if m.Role == msg.RoleAssistant && len(m.ToolCalls()) > 0 {
		ev.GroupID = ev.ID
	}
	ev.ContentType = inferContentType(m, p.call)
	ev.ExitCode = m.Meta.ExitCode
	if ev.ExitCode != nil && *ev.ExitCode != 0 {
		ev.IsError = true
	}
	meta := storedMeta{Host: m.Meta, Gen: a.gen}
	cmd := m.Meta.Command
	if cmd == "" {
		cmd = commandFromInput(p.call.input)
	}
	if cmd != "" {
		meta.CmdKey = textproc.CommandKey(cmd)
	}
	var paths []string
	paths = append(paths, m.Meta.Paths...)
	paths = append(paths, pathsFromInput(p.call.input)...)
	p.primary = map[string]bool{}
	for _, x := range dedupeStrings(paths) {
		p.primary[x] = true
		p.primaryList = append(p.primaryList, x)
	}
	for _, pt := range m.ToolCalls() {
		paths = append(paths, pathsFromInput(pt.Input)...)
	}
	text := m.PlainText()
	ev.RawBytes = len(text)
	if ev.ContentType != msg.CTFileRead && ev.ContentType != msg.CTRetrieval {
		paths = append(paths, textproc.Paths(text, 16)...)
	}
	ev.Paths = dedupeStrings(paths)
	if ev.ContentType != msg.CTRetrieval {
		ev.Symbols = textproc.Symbols(text, 48)
	}
	if m.Role == msg.RoleTool {
		if tr, ok := m.ToolResultPart(); ok {
			meta.TextHash = store.HashBytes([]byte(tr.Text))
		}
		lines := strings.Count(text, "\n") + 1
		meta.Lines = lines
		if ev.ContentType == msg.CTCommand || ev.ContentType == msg.CTTest || ev.IsError || lines > 40 {
			d := textproc.DigestLog(text, a.pol.Masking.DigestErrors)
			meta.Digest = &d
		}
		if ev.ContentType == msg.CTFileRead {
			fh := m.Meta.FileHash
			if fh == "" {
				fh = store.HashBytes([]byte(text))
			}
			meta.FileHash = fh
		}
		if a.pol.Masking.Externalize && ev.Tokens > a.pol.Budget.MaxObservationToken {
			meta.Oversize = true
			pv := textproc.HeadTail(text, a.pol.Masking.PreviewHead, a.pol.Masking.PreviewTail, a.pol.Masking.PreviewLineMax)
			meta.Preview = formatPreview(pv)
		}
	}
	if m.Role == msg.RoleUser && ev.Tokens > a.pol.Budget.MaxObservationToken && a.pol.Masking.Externalize {
		meta.Oversize = true
		pv := textproc.HeadTail(text, a.pol.Masking.PreviewHead, a.pol.Masking.PreviewTail, a.pol.Masking.PreviewLineMax)
		meta.Preview = formatPreview(pv)
	}
	for _, part := range m.Parts {
		sp := storedPart{Part: part}
		switch {
		case len(part.Text) > inlineMax:
			h := store.HashBytes([]byte(part.Text))
			p.blobs[h] = []byte(part.Text)
			sp.Blob, sp.BlobField, sp.BlobSize = h, "text", len(part.Text)
			sp.Text = ""
		case len(part.Input) > inlineMax:
			h := store.HashBytes(part.Input)
			p.blobs[h] = append([]byte(nil), part.Input...)
			sp.Blob, sp.BlobField, sp.BlobSize = h, "input", len(part.Input)
			sp.Input = nil
		case len(part.Data) > inlineMax:
			h := store.HashBytes([]byte(part.Data))
			p.blobs[h] = []byte(part.Data)
			sp.Blob, sp.BlobField, sp.BlobSize = h, "data", len(part.Data)
			sp.Data = ""
		}
		p.body = append(p.body, sp)
	}
	p.ev = ev
	p.meta = meta
	p.parts = m.Parts
	return p, nil
}

func formatPreview(pv textproc.Preview) string {
	if pv.Omitted == 0 {
		return pv.Head
	}
	return fmt.Sprintf("%s\n[... %d lines omitted ...]\n%s", pv.Head, pv.Omitted, pv.Tail)
}

func dedupeStrings(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		x = strings.TrimSpace(x)
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

func (a *Agent) writeEvent(ctx context.Context, tx *sql.Tx, p *appendPlan) error {
	hashes := make([]string, 0, len(p.blobs))
	for h := range p.blobs {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)
	for _, h := range hashes {
		if err := a.e.putBlob(ctx, tx, h, p.blobs[h]); err != nil {
			return err
		}
	}
	body, err := json.Marshal(p.body)
	if err != nil {
		return err
	}
	meta, _ := json.Marshal(p.meta)
	paths, _ := json.Marshal(p.ev.Paths)
	syms, _ := json.Marshal(p.ev.Symbols)
	ev := p.ev
	var exit any
	if ev.ExitCode != nil {
		exit = *ev.ExitCode
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO events(id,session_id,agent_id,seq,parent_id,cause_id,ts,role,content_type,tool_name,tool_call_id,group_id,exit_code,is_error,body,content_hash,tokens,raw_bytes,scope,paths,symbols,meta,protected)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		ev.ID, ev.SessionID, ev.AgentID, ev.Seq, nullable(ev.ParentID), nullable(ev.CauseID), ev.TS, string(ev.Role), string(ev.ContentType),
		nullable(ev.ToolName), nullable(ev.ToolCallID), nullable(ev.GroupID), exit, boolInt(ev.IsError), body, ev.ContentHash, ev.Tokens, ev.RawBytes,
		ev.Scope, string(paths), string(syms), string(meta), boolInt(ev.Protected))
	if err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO blob_refs(blob_hash,session_id,agent_id,event_id) VALUES(?,?,?,?)", h, ev.SessionID, ev.AgentID, ev.ID); err != nil {
			return err
		}
	}
	op := "mention"
	switch ev.ContentType {
	case msg.CTFileRead:
		op = "read"
	case msg.CTFileWrite:
		op = "write"
	}
	for _, path := range ev.Paths {
		pop := op
		if !p.primary[path] {
			pop = "mention"
		}
		fh := ""
		if pop == "read" {
			fh = p.meta.FileHash
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO paths(session_id,agent_id,path,event_id,seq,op,content_hash) VALUES(?,?,?,?,?,?,?)", ev.SessionID, ev.AgentID, path, ev.ID, ev.Seq, pop, fh); err != nil {
			return err
		}
	}
	for _, s := range ev.Symbols {
		if _, err := tx.ExecContext(ctx, "INSERT INTO symbols(session_id,agent_id,name,lname,event_id,seq) VALUES(?,?,?,?,?,?)", ev.SessionID, ev.AgentID, s, strings.ToLower(s), ev.ID, ev.Seq); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (a *Agent) trackFiles(it *item) {
	ev := it.ev
	if ev.Role != msg.RoleTool || len(it.primary) == 0 {
		return
	}
	switch ev.ContentType {
	case msg.CTFileRead:
		path := it.primary[0]
		it.readPath = path
		a.lastRead[path] = readInfo{seq: ev.Seq, eventID: ev.ID, fileHash: it.meta.FileHash}
	case msg.CTFileWrite:
		if ev.IsError {
			return
		}
		for _, path := range it.primary {
			a.lastWrite[path] = ev.Seq
		}
	}
}

func scanEvent(rows interface{ Scan(...any) error }) (Event, []storedPart, storedMeta, error) {
	var ev Event
	var parent, cause, tool, call, group sql.NullString
	var exit sql.NullInt64
	var isErr, prot, purged int
	var body []byte
	var paths, syms, meta string
	err := rows.Scan(&ev.ID, &ev.SessionID, &ev.AgentID, &ev.Seq, &parent, &cause, &ev.TS, &ev.Role, &ev.ContentType, &tool, &call, &group, &exit, &isErr, &body, &ev.ContentHash, &ev.Tokens, &ev.RawBytes, &ev.Scope, &paths, &syms, &meta, &prot, &purged)
	if err != nil {
		return ev, nil, storedMeta{}, err
	}
	ev.ParentID, ev.CauseID, ev.ToolName, ev.ToolCallID, ev.GroupID = parent.String, cause.String, tool.String, call.String, group.String
	if exit.Valid {
		v := int(exit.Int64)
		ev.ExitCode = &v
	}
	ev.IsError, ev.Protected, ev.Purged = isErr == 1, prot == 1, purged == 1
	_ = json.Unmarshal([]byte(paths), &ev.Paths)
	_ = json.Unmarshal([]byte(syms), &ev.Symbols)
	var sm storedMeta
	_ = json.Unmarshal([]byte(meta), &sm)
	var parts []storedPart
	if len(body) > 0 {
		if err := json.Unmarshal(body, &parts); err != nil {
			return ev, nil, sm, fmt.Errorf("engine: event %s body: %w", ev.ID, err)
		}
	}
	return ev, parts, sm, nil
}

const eventCols = "id,session_id,agent_id,seq,parent_id,cause_id,ts,role,content_type,tool_name,tool_call_id,group_id,exit_code,is_error,body,content_hash,tokens,raw_bytes,scope,paths,symbols,meta,protected,purged"

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (e *Engine) resolveParts(ctx context.Context, q queryer, parts []storedPart) ([]msg.Part, error) {
	out := make([]msg.Part, len(parts))
	for i, sp := range parts {
		p := sp.Part
		if sp.Blob != "" {
			data, err := e.getBlob(ctx, q, sp.Blob)
			if err != nil {
				return nil, err
			}
			switch sp.BlobField {
			case "text":
				p.Text = string(data)
			case "input":
				p.Input = json.RawMessage(data)
			case "data":
				p.Data = string(data)
			}
		}
		out[i] = p
	}
	return out, nil
}

func reconstruct(ev Event, parts []msg.Part, sm storedMeta) msg.Message {
	return msg.Message{Role: ev.Role, Parts: parts, Meta: sm.Host}
}

func (e *Engine) loadEvent(ctx context.Context, q queryer, id string) (Event, msg.Message, storedMeta, error) {
	row := q.QueryRowContext(ctx, "SELECT "+eventCols+" FROM events WHERE id=?", id)
	ev, sp, sm, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, msg.Message{}, sm, fmt.Errorf("%w: event %s", ErrNotFound, id)
	}
	if err != nil {
		return ev, msg.Message{}, sm, err
	}
	if ev.Purged {
		return ev, msg.Message{}, sm, fmt.Errorf("%w: event %s was purged by retention policy", ErrNotFound, id)
	}
	parts, err := e.resolveParts(ctx, q, sp)
	if err != nil {
		return ev, msg.Message{}, sm, err
	}
	return ev, reconstruct(ev, parts, sm), sm, nil
}

func (e *Engine) VerifyEvent(ctx context.Context, id string) error {
	ev, m, _, err := e.loadEvent(ctx, e.db.R, id)
	if err != nil {
		return err
	}
	h, _, err := canonicalHash(m)
	if err != nil {
		return err
	}
	if h != ev.ContentHash {
		return fmt.Errorf("engine: event %s content hash mismatch", id)
	}
	return nil
}
