package msg

import (
	"encoding/json"
	"errors"
	"fmt"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

type PartType string

const (
	PartText             PartType = "text"
	PartToolCall         PartType = "tool_call"
	PartToolResult       PartType = "tool_result"
	PartThinking         PartType = "thinking"
	PartRedactedThinking PartType = "redacted_thinking"
	PartOpaque           PartType = "opaque"
)

type Part struct {
	Type       PartType        `json:"type"`
	Text       string          `json:"text,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	Signature  string          `json:"signature,omitempty"`
	Data       string          `json:"data,omitempty"`
	Provider   string          `json:"provider,omitempty"`
}

type ContentType string

const (
	CTText      ContentType = "text"
	CTFileRead  ContentType = "file_read"
	CTFileWrite ContentType = "file_write"
	CTCommand   ContentType = "command"
	CTTest      ContentType = "test"
	CTSearch    ContentType = "search"
	CTRetrieval ContentType = "retrieval"
	CTHandoff   ContentType = "handoff"
	CTJSON      ContentType = "json"
)

type Meta struct {
	ContentType ContentType `json:"content_type,omitempty"`
	Paths       []string    `json:"paths,omitempty"`
	Command     string      `json:"command,omitempty"`
	ExitCode    *int        `json:"exit_code,omitempty"`
	Protected   bool        `json:"protected,omitempty"`
	Constraint  bool        `json:"constraint,omitempty"`
	FileHash    string      `json:"file_hash,omitempty"`
	Shared      bool        `json:"shared,omitempty"`
	CauseID     string      `json:"cause_id,omitempty"`
}

type Message struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
	Meta  Meta   `json:"meta"`
}

func Text(role Role, s string) Message {
	return Message{Role: role, Parts: []Part{{Type: PartText, Text: s}}}
}

func ToolResult(callID, toolName, text string, isError bool, meta Meta) Message {
	return Message{Role: RoleTool, Parts: []Part{{Type: PartToolResult, ToolCallID: callID, ToolName: toolName, Text: text, IsError: isError}}, Meta: meta}
}

func (m Message) ToolCalls() []Part {
	var out []Part
	for _, p := range m.Parts {
		if p.Type == PartToolCall {
			out = append(out, p)
		}
	}
	return out
}

func (m Message) ToolResultPart() (Part, bool) {
	for _, p := range m.Parts {
		if p.Type == PartToolResult {
			return p, true
		}
	}
	return Part{}, false
}

func (m Message) PlainText() string {
	var n int
	for _, p := range m.Parts {
		n += len(p.Text)
	}
	b := make([]byte, 0, n+len(m.Parts))
	for i, p := range m.Parts {
		if p.Type == PartThinking || p.Type == PartRedactedThinking || p.Type == PartOpaque {
			continue
		}
		if i > 0 && len(b) > 0 {
			b = append(b, '\n')
		}
		if p.Type == PartToolCall {
			b = append(b, p.ToolName...)
			b = append(b, ' ')
			b = append(b, p.Input...)
			continue
		}
		b = append(b, p.Text...)
	}
	return string(b)
}

var (
	ErrEmptyMessage     = errors.New("msg: message has no parts")
	ErrBadRole          = errors.New("msg: invalid role")
	ErrToolResultShape  = errors.New("msg: tool message must carry exactly one tool_result part")
	ErrMissingCallID    = errors.New("msg: tool call or result without id")
	ErrMisplacedPart    = errors.New("msg: part type not allowed for role")
	ErrDuplicateCallID  = errors.New("msg: duplicate tool call id")
	ErrOrphanToolResult = errors.New("msg: tool result without matching open tool call")
	ErrUnansweredCall   = errors.New("msg: tool call without result before next turn")
)

func (m Message) Validate() error {
	if len(m.Parts) == 0 {
		return ErrEmptyMessage
	}
	switch m.Role {
	case RoleUser, RoleSystem:
		for _, p := range m.Parts {
			if p.Type != PartText {
				return fmt.Errorf("%w: %s in %s", ErrMisplacedPart, p.Type, m.Role)
			}
		}
	case RoleAssistant:
		seen := map[string]bool{}
		for _, p := range m.Parts {
			switch p.Type {
			case PartText, PartThinking, PartRedactedThinking, PartOpaque:
			case PartToolCall:
				if p.ToolCallID == "" || p.ToolName == "" {
					return ErrMissingCallID
				}
				if seen[p.ToolCallID] {
					return ErrDuplicateCallID
				}
				seen[p.ToolCallID] = true
			default:
				return fmt.Errorf("%w: %s in assistant", ErrMisplacedPart, p.Type)
			}
		}
	case RoleTool:
		if len(m.Parts) != 1 || m.Parts[0].Type != PartToolResult {
			return ErrToolResultShape
		}
		if m.Parts[0].ToolCallID == "" {
			return ErrMissingCallID
		}
	default:
		return ErrBadRole
	}
	return nil
}

type PairingState struct {
	open  map[string]bool
	order []string
}

func NewPairingState() *PairingState { return &PairingState{open: map[string]bool{}} }

func (s *PairingState) Clone() *PairingState {
	c := &PairingState{open: make(map[string]bool, len(s.open)), order: append([]string(nil), s.order...)}
	for k, v := range s.open {
		c.open[k] = v
	}
	return c
}

func (s *PairingState) Open() []string {
	var out []string
	for _, id := range s.order {
		if s.open[id] {
			out = append(out, id)
		}
	}
	return out
}

func (s *PairingState) Step(m Message) error {
	switch m.Role {
	case RoleAssistant:
		if len(s.Open()) > 0 {
			return ErrUnansweredCall
		}
		s.open = map[string]bool{}
		s.order = s.order[:0]
		for _, p := range m.ToolCalls() {
			s.open[p.ToolCallID] = true
			s.order = append(s.order, p.ToolCallID)
		}
	case RoleTool:
		p, _ := m.ToolResultPart()
		if !s.open[p.ToolCallID] {
			return ErrOrphanToolResult
		}
		s.open[p.ToolCallID] = false
	case RoleUser:
		if len(s.Open()) > 0 {
			return ErrUnansweredCall
		}
	}
	return nil
}

func ValidateSequence(ms []Message) error {
	st := NewPairingState()
	for i, m := range ms {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("message %d: %w", i, err)
		}
		if err := st.Step(m); err != nil {
			return fmt.Errorf("message %d: %w", i, err)
		}
	}
	return nil
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
}

type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
}

func (u Usage) TotalInput() int { return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens }
