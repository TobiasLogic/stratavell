package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
	"github.com/TobiasLogic/palimpsest/summarize"
)

type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type Block struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	Data         string          `json:"data,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      any             `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
}

type Message struct {
	Role    string  `json:"role"`
	Content []Block `json:"content"`
}

type Tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *CacheControl   `json:"cache_control,omitempty"`
}

type Request struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    []Block         `json:"system,omitempty"`
	Tools     []Tool          `json:"tools,omitempty"`
	Messages  []Message       `json:"messages"`
	Thinking  json.RawMessage `json:"thinking,omitempty"`
	Extra     map[string]any  `json:"-"`
}

func (r Request) MarshalJSON() ([]byte, error) {
	type plain Request
	b, err := json.Marshal(plain(r))
	if err != nil || len(r.Extra) == 0 {
		return b, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		if _, taken := m[k]; !taken {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

type Options struct {
	Model     string
	MaxTokens int
	CacheTTL  string
	Thinking  json.RawMessage
	Extra     map[string]any
}

var ErrInvalid = errors.New("anthropic: invalid request")

type pos struct{ msg, block int }

func Render(c *engine.Context, o Options) (Request, error) {
	req := Request{Model: o.Model, MaxTokens: o.MaxTokens, Thinking: o.Thinking, Extra: o.Extra}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 4096
	}
	cc := &CacheControl{Type: "ephemeral", TTL: o.CacheTTL}
	for _, s := range c.System {
		if s.Text != "" {
			req.System = append(req.System, Block{Type: "text", Text: s.Text})
		}
	}
	for _, t := range c.Tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		req.Tools = append(req.Tools, Tool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	where := map[[2]int]pos{}
	for mi, m := range c.Messages {
		role := "user"
		if m.Role == msg.RoleAssistant {
			role = "assistant"
		}
		var blocks []Block
		var idx []int
		for pi, p := range m.Parts {
			b, ok := block(p, m.KeepThinking)
			if !ok {
				continue
			}
			blocks = append(blocks, b)
			idx = append(idx, pi)
		}
		if len(blocks) == 0 {
			continue
		}
		n := len(req.Messages)
		if n > 0 && req.Messages[n-1].Role == role {
			prev := &req.Messages[n-1]
			if role == "user" && blocks[0].Type == "tool_result" && hasNonToolResult(prev.Content) {
				return req, fmt.Errorf("%w: tool_result after user text in message %d", ErrInvalid, mi)
			}
			base := len(prev.Content)
			prev.Content = append(prev.Content, blocks...)
			for k, pi := range idx {
				where[[2]int{mi, pi}] = pos{n - 1, base + k}
			}
			continue
		}
		req.Messages = append(req.Messages, Message{Role: role, Content: blocks})
		for k, pi := range idx {
			where[[2]int{mi, pi}] = pos{n, k}
		}
	}
	if len(req.Messages) == 0 || req.Messages[0].Role != "user" {
		return req, fmt.Errorf("%w: the first message must be a user message", ErrInvalid)
	}
	used := 0
	for _, bp := range c.Breakpoints {
		if used >= 4 {
			break
		}
		if bp.Message < 0 {
			if len(req.System) > 0 {
				req.System[len(req.System)-1].CacheControl = cc
				used++
			} else if len(req.Tools) > 0 {
				req.Tools[len(req.Tools)-1].CacheControl = cc
				used++
			}
			continue
		}
		p, ok := where[[2]int{bp.Message, bp.Part}]
		for back := bp.Part - 1; !ok && back >= 0; back-- {
			p, ok = where[[2]int{bp.Message, back}]
		}
		if !ok {
			continue
		}
		blk := &req.Messages[p.msg].Content[p.block]
		for blk.Type == "thinking" || blk.Type == "redacted_thinking" {
			if p.block == 0 {
				blk = nil
				break
			}
			p.block--
			blk = &req.Messages[p.msg].Content[p.block]
		}
		if blk != nil && blk.CacheControl == nil {
			blk.CacheControl = cc
			used++
		}
	}
	return req, validate(req)
}

func hasNonToolResult(bs []Block) bool {
	for _, b := range bs {
		if b.Type != "tool_result" {
			return true
		}
	}
	return false
}

func block(p msg.Part, keepThinking bool) (Block, bool) {
	switch p.Type {
	case msg.PartText:
		if p.Text == "" {
			return Block{}, false
		}
		return Block{Type: "text", Text: p.Text}, true
	case msg.PartThinking:
		if !keepThinking || (p.Provider != "" && p.Provider != "anthropic") {
			return Block{}, false
		}
		return Block{Type: "thinking", Thinking: p.Text, Signature: p.Signature}, true
	case msg.PartRedactedThinking:
		if !keepThinking || (p.Provider != "" && p.Provider != "anthropic") {
			return Block{}, false
		}
		return Block{Type: "redacted_thinking", Data: p.Data}, true
	case msg.PartToolCall:
		in := p.Input
		if len(in) == 0 {
			in = json.RawMessage(`{}`)
		}
		return Block{Type: "tool_use", ID: p.ToolCallID, Name: p.ToolName, Input: in}, true
	case msg.PartToolResult:
		text := p.Text
		if text == "" {
			text = "(no output)"
		}
		return Block{Type: "tool_result", ToolUseID: p.ToolCallID, Content: text, IsError: p.IsError}, true
	case msg.PartOpaque:
		if p.Provider != "anthropic" || p.Data == "" {
			return Block{}, false
		}
		var b Block
		if json.Unmarshal([]byte(p.Data), &b) != nil {
			return Block{}, false
		}
		return b, true
	}
	return Block{}, false
}

func validate(r Request) error {
	open := map[string]bool{}
	for i, m := range r.Messages {
		if i > 0 && r.Messages[i-1].Role == m.Role {
			return fmt.Errorf("%w: consecutive %s messages at %d", ErrInvalid, m.Role, i)
		}
		if m.Role == "user" {
			seenOther := false
			for _, b := range m.Content {
				if b.Type == "tool_result" {
					if seenOther {
						return fmt.Errorf("%w: tool_result must come first in message %d", ErrInvalid, i)
					}
					if !open[b.ToolUseID] {
						return fmt.Errorf("%w: tool_result %s has no matching tool_use", ErrInvalid, b.ToolUseID)
					}
					delete(open, b.ToolUseID)
				} else {
					seenOther = true
				}
			}
			if len(open) > 0 {
				return fmt.Errorf("%w: tool_use without tool_result before message %d", ErrInvalid, i)
			}
		} else {
			if len(open) > 0 {
				return fmt.Errorf("%w: unanswered tool_use before assistant message %d", ErrInvalid, i)
			}
			for _, b := range m.Content {
				if b.Type == "tool_use" {
					open[b.ID] = true
				}
			}
		}
	}
	if len(open) > 0 {
		return fmt.Errorf("%w: request ends with tool_use blocks that have no tool_result", ErrInvalid)
	}
	n := 0
	for _, b := range r.System {
		if b.CacheControl != nil {
			n++
		}
	}
	for _, t := range r.Tools {
		if t.CacheControl != nil {
			n++
		}
	}
	for _, m := range r.Messages {
		for _, b := range m.Content {
			if b.CacheControl != nil {
				n++
			}
		}
	}
	if n > 4 {
		return fmt.Errorf("%w: %d cache breakpoints (max 4)", ErrInvalid, n)
	}
	return nil
}

type Response struct {
	ID         string  `json:"id"`
	Model      string  `json:"model"`
	StopReason string  `json:"stop_reason"`
	Content    []Block `json:"content"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	} `json:"usage"`
}

func Parse(r Response) (msg.Message, msg.Usage) {
	m := msg.Message{Role: msg.RoleAssistant}
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			m.Parts = append(m.Parts, msg.Part{Type: msg.PartText, Text: b.Text})
		case "thinking":
			m.Parts = append(m.Parts, msg.Part{Type: msg.PartThinking, Text: b.Thinking, Signature: b.Signature, Provider: "anthropic"})
		case "redacted_thinking":
			m.Parts = append(m.Parts, msg.Part{Type: msg.PartRedactedThinking, Data: b.Data, Provider: "anthropic"})
		case "tool_use":
			in := b.Input
			if len(in) == 0 {
				in = json.RawMessage(`{}`)
			}
			m.Parts = append(m.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: b.ID, ToolName: b.Name, Input: in})
		default:
			raw, _ := json.Marshal(b)
			m.Parts = append(m.Parts, msg.Part{Type: msg.PartOpaque, Data: string(raw), Provider: "anthropic"})
		}
	}
	if len(m.Parts) == 0 {
		m.Parts = []msg.Part{{Type: msg.PartText, Text: ""}}
	}
	u := msg.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens, CacheReadTokens: r.Usage.CacheReadInputTokens, CacheCreationTokens: r.Usage.CacheCreationInputTokens}
	return m, u
}

type Client struct {
	BaseURL string
	APIKey  string
	Version string
	Betas   []string
	HTTP    *http.Client
	Options Options
}

func NewClient(model string) *Client {
	return &Client{BaseURL: "https://api.anthropic.com", APIKey: os.Getenv("ANTHROPIC_API_KEY"), Version: "2023-06-01", HTTP: &http.Client{Timeout: 10 * time.Minute}, Options: Options{Model: model, MaxTokens: 8192}}
}

type APIError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("anthropic: HTTP %d: %s", e.Status, e.Body) }

func (c *Client) Send(ctx context.Context, req Request) (Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	hr, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	hr.Header.Set("content-type", "application/json")
	hr.Header.Set("x-api-key", c.APIKey)
	hr.Header.Set("anthropic-version", c.Version)
	for _, b := range c.Betas {
		hr.Header.Add("anthropic-beta", b)
	}
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return Response{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Response{}, err
	}
	if resp.StatusCode/100 != 2 {
		ae := &APIError{Status: resp.StatusCode, Body: string(data)}
		if s, err := strconv.Atoi(resp.Header.Get("retry-after")); err == nil {
			ae.RetryAfter = time.Duration(s) * time.Second
		}
		return Response{}, ae
	}
	var out Response
	return out, json.Unmarshal(data, &out)
}

func (c *Client) Generate(ctx context.Context, cx *engine.Context) (msg.Message, msg.Usage, error) {
	req, err := Render(cx, c.Options)
	if err != nil {
		return msg.Message{}, msg.Usage{}, err
	}
	resp, err := c.Send(ctx, req)
	if err != nil {
		return msg.Message{}, msg.Usage{}, err
	}
	m, u := Parse(resp)
	return m, u, nil
}

func (c *Client) Complete(ctx context.Context, system, user string, maxTokens int) (summarize.Completion, error) {
	req := Request{Model: c.Options.Model, MaxTokens: maxTokens, System: []Block{{Type: "text", Text: system}}, Messages: []Message{{Role: "user", Content: []Block{{Type: "text", Text: user}}}}}
	resp, err := c.Send(ctx, req)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 429 {
			return summarize.Completion{}, &engine.RateLimitError{RetryAfterMS: ae.RetryAfter.Milliseconds()}
		}
		return summarize.Completion{}, err
	}
	var text string
	for _, b := range resp.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	return summarize.Completion{Text: text, Model: resp.Model, InputTokens: resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens, OutputTokens: resp.Usage.OutputTokens}, nil
}

type CountRequest struct {
	Model    string    `json:"model"`
	System   []Block   `json:"system,omitempty"`
	Tools    []Tool    `json:"tools,omitempty"`
	Messages []Message `json:"messages"`
}

func (c *Client) CountTokens(ctx context.Context, req Request) (int, error) {
	body, _ := json.Marshal(CountRequest{Model: req.Model, System: req.System, Tools: req.Tools, Messages: req.Messages})
	hr, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/messages/count_tokens", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	hr.Header.Set("content-type", "application/json")
	hr.Header.Set("x-api-key", c.APIKey)
	hr.Header.Set("anthropic-version", c.Version)
	resp, err := c.HTTP.Do(hr)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return 0, &APIError{Status: resp.StatusCode, Body: string(data)}
	}
	var out struct {
		InputTokens int `json:"input_tokens"`
	}
	return out.InputTokens, json.Unmarshal(data, &out)
}
