package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TobiasLogic/palimpsest/internal/textproc"
	"github.com/TobiasLogic/palimpsest/msg"
)

type renderMode string

const (
	modeFull      renderMode = "full"
	modePreview   renderMode = "preview"
	modeMasked    renderMode = "masked"
	modeDuplicate renderMode = "duplicate"
	modeUnchanged renderMode = "unchanged"
	modeInputMask renderMode = "input_masked"
)

type rendered struct {
	m        msg.Message
	src      *item
	mode     renderMode
	tokens   int
	modified bool
}

type renderState struct {
	maskSeq      int64
	exempt       map[string]bool
	seenText     map[string]string
	seenRead     map[string]readInfo
	writesAfter  map[string]int64
	rollingFloor int64
	squeeze      bool
}

func (a *Agent) newRenderState() *renderState {
	rs := &renderState{
		maskSeq:     a.maskSeq,
		exempt:      map[string]bool{},
		seenText:    map[string]string{},
		seenRead:    map[string]readInfo{},
		writesAfter: map[string]int64{},
	}
	for _, p := range a.pinned {
		if p.exemptEvent != "" {
			rs.exempt[p.exemptEvent] = true
		}
	}
	if a.pol.Masking.Mode == MaskRolling {
		keep := a.pol.Masking.RollingKeep
		n := 0
		rs.maskSeq = 0
		for i := len(a.raw) - 1; i >= 0; i-- {
			if a.raw[i].isObservation() {
				n++
				if n > keep {
					rs.maskSeq = a.raw[i].ev.Seq
					break
				}
			}
		}
	}
	if a.pol.Masking.Mode == MaskOff {
		rs.maskSeq = 0
	}
	rs.squeeze = a.squeeze
	if a.emergencyMask > rs.maskSeq {
		rs.maskSeq = a.emergencyMask
	}
	return rs
}

var sentinelTags = []string{"<standing_instructions", "</standing_instructions", "<archive", "</archive", "<task_state", "</task_state", "<retrieved_evidence", "</retrieved_evidence"}

func needsEscape(parts []msg.Part) bool {
	for _, p := range parts {
		for _, t := range sentinelTags {
			if strings.Contains(p.Text, t) {
				return true
			}
		}
	}
	return false
}

func escapeParts(ps []msg.Part) []msg.Part {
	out := make([]msg.Part, len(ps))
	for i, p := range ps {
		q := p
		q.Text = sanitizeArchiveText(p.Text)
		out[i] = q
	}
	return out
}

func (a *Agent) squeezedObservation(it *item) msg.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "[observation shortened under context pressure: %s]\n", a.obsHeader(it))
	if d := it.meta.Digest; d != nil {
		b.WriteString(digestLine(d))
		b.WriteString("\n")
	}
	var text string
	for _, p := range it.parts {
		text += p.Text
	}
	if text == "" {
		text = it.meta.Preview
	}
	b.WriteString(formatPreview(textproc.HeadTail(text, 8, 8, 160)))
	fmt.Fprintf(&b, "\n[memory_get_event(%q) returns the original]", it.ev.ID)
	return a.stubObservation(it, b.String())
}

func (a *Agent) renderRaw() []rendered {
	rs := a.newRenderState()
	out := make([]rendered, 0, len(a.raw))
	lastWriteSeq := map[string]int64{}
	for _, it := range a.raw {
		r := a.renderItem(it, rs, lastWriteSeq)
		out = append(out, r)
		if it.ev.ContentType == msg.CTFileWrite && !it.ev.IsError {
			for _, p := range it.primary {
				lastWriteSeq[p] = it.ev.Seq
			}
		}
	}
	return out
}

func (a *Agent) renderItem(it *item, rs *renderState, lastWriteSeq map[string]int64) rendered {
	ev := it.ev
	pol := a.pol.Masking
	masked := ev.Seq <= rs.maskSeq && !ev.Protected
	if ev.Role == msg.RoleTool {
		tr := it.parts
		textHash := it.meta.TextHash
		if masked && !rs.exempt[ev.ID] {
			m := a.maskedObservation(it)
			return rendered{m: m, src: it, mode: modeMasked, tokens: a.e.messageTokens(m), modified: true}
		}
		if masked && rs.exempt[ev.ID] {
			m := a.previewObservation(it, true)
			return rendered{m: m, src: it, mode: modePreview, tokens: a.e.messageTokens(m), modified: true}
		}
		if pol.Dedupe && textHash != "" && it.ev.Tokens > 120 && ev.ContentType != msg.CTFileRead {
			if prev, ok := rs.seenText[textHash]; ok {
				m := a.stubObservation(it, fmt.Sprintf("[identical to the output of %s (sha256 %s); not repeated]", prev, short(textHash)))
				return rendered{m: m, src: it, mode: modeDuplicate, tokens: a.e.messageTokens(m), modified: true}
			}
		}
		if pol.UnchangedReads && ev.ContentType == msg.CTFileRead && it.readPath != "" && it.meta.FileHash != "" && it.ev.Tokens > 120 {
			if prev, ok := rs.seenRead[it.readPath]; ok && prev.fileHash == it.meta.FileHash && lastWriteSeq[it.readPath] < prev.seq {
				m := a.stubObservation(it, fmt.Sprintf("[file %s unchanged since it was read in %s (sha256 %s); content not repeated]", it.readPath, prev.eventID, short(it.meta.FileHash)))
				return rendered{m: m, src: it, mode: modeUnchanged, tokens: a.e.messageTokens(m), modified: true}
			}
		}
		if it.meta.Oversize {
			m := a.previewObservation(it, false)
			return rendered{m: m, src: it, mode: modePreview, tokens: a.e.messageTokens(m), modified: true}
		}
		if textHash != "" {
			rs.seenText[textHash] = ev.ID
		}
		if ev.ContentType == msg.CTFileRead && it.readPath != "" {
			rs.seenRead[it.readPath] = readInfo{seq: ev.Seq, eventID: ev.ID, fileHash: it.meta.FileHash}
		}
		if rs.squeeze && ev.Tokens > 400 {
			m := a.squeezedObservation(it)
			return rendered{m: m, src: it, mode: modePreview, tokens: a.e.messageTokens(m), modified: true}
		}
		if it.escape {
			m := msg.Message{Role: ev.Role, Parts: escapeParts(tr), Meta: it.meta.Host}
			return rendered{m: m, src: it, mode: modeFull, tokens: ev.Tokens}
		}
		m := msg.Message{Role: ev.Role, Parts: tr, Meta: it.meta.Host}
		return rendered{m: m, src: it, mode: modeFull, tokens: ev.Tokens}
	}
	if ev.Role == msg.RoleUser && it.meta.Oversize {
		text := fmt.Sprintf("[large user message %s externalized: %d tokens, %d bytes. Directive sentences and the preview follow; memory_get_event(%q) returns the full text]\n", ev.ID, ev.Tokens, ev.RawBytes, ev.ID)
		if ds := textproc.DirectiveSentences(it.meta.Preview, 12); len(ds) > 0 {
			text += "directives:\n- " + strings.Join(ds, "\n- ") + "\n"
		}
		text += it.meta.Preview
		m := msg.Text(msg.RoleUser, text)
		return rendered{m: m, src: it, mode: modePreview, tokens: a.e.messageTokens(m), modified: true}
	}
	if ev.Role == msg.RoleAssistant && (masked || rs.squeeze) && (pol.MaskInputs || rs.squeeze) && (pol.Mode != MaskOff || rs.squeeze) {
		changed := false
		parts := make([]msg.Part, 0, len(it.parts))
		for _, p := range it.parts {
			if p.Type == msg.PartToolCall && a.e.count(string(p.Input)) > pol.InputMaskMin {
				parts = append(parts, maskInput(p, ev.ID))
				changed = true
				continue
			}
			parts = append(parts, p)
		}
		if changed {
			m := msg.Message{Role: ev.Role, Parts: parts, Meta: it.meta.Host}
			return rendered{m: m, src: it, mode: modeInputMask, tokens: a.e.messageTokens(m), modified: true}
		}
	}
	m := msg.Message{Role: ev.Role, Parts: it.parts, Meta: it.meta.Host}
	return rendered{m: m, src: it, mode: modeFull, tokens: ev.Tokens}
}

func maskInput(p msg.Part, evID string) msg.Part {
	keep := map[string]any{}
	var orig map[string]any
	if json.Unmarshal(p.Input, &orig) == nil {
		for _, k := range []string{"path", "file_path", "command", "cmd", "pattern", "query"} {
			if v, ok := orig[k]; ok {
				if s, ok := v.(string); ok && len(s) <= 300 {
					keep[k] = s
				}
			}
		}
	}
	keep["_archived_input"] = fmt.Sprintf("input of this call (%d bytes) archived in %s; memory_get_event returns it", len(p.Input), evID)
	b, _ := json.Marshal(keep)
	q := p
	q.Input = b
	return q
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func (a *Agent) stubObservation(it *item, text string) msg.Message {
	tr, _ := firstResult(it.parts)
	if it.escape {
		text = sanitizeArchiveText(text)
	}
	tr.Text = text
	return msg.Message{Role: msg.RoleTool, Parts: []msg.Part{tr}, Meta: it.meta.Host}
}

func firstResult(parts []msg.Part) (msg.Part, bool) {
	for _, p := range parts {
		if p.Type == msg.PartToolResult {
			q := p
			q.Text = ""
			return q, true
		}
	}
	return msg.Part{Type: msg.PartToolResult}, false
}

func (a *Agent) obsHeader(it *item) string {
	ev := it.ev
	var b strings.Builder
	fmt.Fprintf(&b, "%s", ev.ID)
	if ev.ToolName != "" {
		fmt.Fprintf(&b, " | tool=%s", ev.ToolName)
	}
	if it.meta.CmdKey != "" {
		fmt.Fprintf(&b, " | cmd=%s", textproc.Clip(it.meta.CmdKey, 120))
	}
	if len(it.primary) > 0 {
		fmt.Fprintf(&b, " | path=%s", strings.Join(it.primary, ","))
	}
	if ev.ExitCode != nil {
		fmt.Fprintf(&b, " | exit=%d", *ev.ExitCode)
	} else if ev.IsError {
		b.WriteString(" | error")
	}
	fmt.Fprintf(&b, " | %d lines, ~%d tokens", it.meta.Lines, ev.Tokens)
	if it.meta.FileHash != "" {
		fmt.Fprintf(&b, " | sha256 %s", short(it.meta.FileHash))
	}
	return b.String()
}

func (a *Agent) maskedObservation(it *item) msg.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "[archived observation %s]", a.obsHeader(it))
	if d := it.meta.Digest; d != nil {
		if len(d.FailedTests) > 0 {
			ft := d.FailedTests
			if len(ft) > 3 {
				ft = append(append([]string(nil), ft[:3]...), fmt.Sprintf("+%d more", len(d.FailedTests)-3))
			}
			fmt.Fprintf(&b, "\nfailed: %s", strings.Join(ft, ", "))
		}
		for i, e := range d.Errors {
			if i >= 2 {
				break
			}
			fmt.Fprintf(&b, "\nL%d: %s", e.N, textproc.Clip(e.Text, 160))
		}
		if d.SummaryLine != "" && len(d.Errors) == 0 {
			fmt.Fprintf(&b, "\n%s", d.SummaryLine)
		}
	}
	fmt.Fprintf(&b, "\nmemory_get_event(%q) returns the original.", it.ev.ID)
	return a.stubObservation(it, b.String())
}

func (a *Agent) previewObservation(it *item, activeFailure bool) msg.Message {
	var b strings.Builder
	label := "output externalized"
	if activeFailure {
		label = "active failure evidence kept"
	}
	fmt.Fprintf(&b, "[%s: %s]\n", label, a.obsHeader(it))
	if d := it.meta.Digest; d != nil {
		b.WriteString(textproc.FormatDigest(*d))
		b.WriteString("\n")
	}
	pv := it.meta.Preview
	if pv == "" && len(it.parts) > 0 {
		var text string
		for _, p := range it.parts {
			text += p.Text
		}
		hp := textproc.HeadTail(text, a.pol.Masking.PreviewHead/2, a.pol.Masking.PreviewTail/2, a.pol.Masking.PreviewLineMax)
		pv = formatPreview(hp)
	}
	if pv != "" {
		b.WriteString("--- preview ---\n")
		b.WriteString(pv)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "[full output: memory_get_event(%q), memory_get_artifact for byte ranges, or memory_search]", it.ev.ID)
	return a.stubObservation(it, b.String())
}

func (a *Agent) summarizerText(it *item) string {
	ev := it.ev
	maxTok := a.pol.Compaction.SummarizerObsMax
	switch ev.Role {
	case msg.RoleTool:
		if it.meta.Oversize || ev.Tokens > maxTok {
			var b strings.Builder
			b.WriteString(a.obsHeader(it))
			if d := it.meta.Digest; d != nil {
				b.WriteString("\n")
				b.WriteString(textproc.FormatDigest(*d))
			}
			src := it.meta.Preview
			if src == "" {
				for _, p := range it.parts {
					src += p.Text
				}
			}
			hp := textproc.HeadTail(src, 12, 8, 200)
			b.WriteString("\n")
			b.WriteString(formatPreview(hp))
			return b.String()
		}
		var text string
		for _, p := range it.parts {
			text += p.Text
		}
		return a.obsHeader(it) + "\n" + text
	default:
		var b strings.Builder
		for _, p := range it.parts {
			switch p.Type {
			case msg.PartText:
				b.WriteString(p.Text)
				b.WriteString("\n")
			case msg.PartToolCall:
				in := string(p.Input)
				if a.e.count(in) > maxTok {
					in = textproc.Clip(in, maxTok*3) + " [input clipped]"
				}
				fmt.Fprintf(&b, "[tool call %s %s %s]\n", p.ToolCallID, p.ToolName, in)
			}
		}
		s := b.String()
		if ev.Role == msg.RoleUser && a.e.count(s) > maxTok*3 {
			s = textproc.Clip(s, maxTok*9) + " [message clipped; full text in " + ev.ID + "]"
		}
		if it.meta.Oversize && it.meta.Preview != "" {
			s = it.meta.Preview + "\n[message externalized; full text in " + ev.ID + "]"
		}
		return s
	}
}
