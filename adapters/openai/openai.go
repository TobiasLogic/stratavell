package openai

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
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/msg"
	"github.com/TobiasLogic/palimpsest/summarize"
)

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type Function struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Tool struct {
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Request struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Tools          []Tool          `json:"tools,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	ResponseFormat json.RawMessage `json:"response_format,omitempty"`
	Seed           *int            `json:"seed,omitempty"`
}

type Options struct {
	Model       string
	MaxTokens   int
	Temperature *float64
	Seed        *int
}

var ErrInvalid = errors.New("openai: invalid request")

func str(s string) *string { return &s }

func Render(c *engine.Context, o Options) (Request, error) {
	req := Request{Model: o.Model, MaxTokens: o.MaxTokens, Temperature: o.Temperature, Seed: o.Seed}
	var sys []string
	for _, s := range c.System {
		if s.Text != "" {
			sys = append(sys, s.Text)
		}
	}
	if len(sys) > 0 {
		req.Messages = append(req.Messages, Message{Role: "system", Content: str(strings.Join(sys, "\n\n"))})
	}
	for _, t := range c.Tools {
		params := t.Schema
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		req.Tools = append(req.Tools, Tool{Type: "function", Function: Function{Name: t.Name, Description: t.Description, Parameters: params}})
	}
	for _, m := range c.Messages {
		switch m.Role {
		case msg.RoleTool:
			for _, p := range m.Parts {
				if p.Type == msg.PartToolResult {
					text := p.Text
					if p.IsError && !strings.HasPrefix(text, "error") {
						text = "error: " + text
					}
					req.Messages = append(req.Messages, Message{Role: "tool", ToolCallID: p.ToolCallID, Content: str(text)})
				}
			}
		case msg.RoleAssistant:
			am := Message{Role: "assistant"}
			var text []string
			for _, p := range m.Parts {
				switch p.Type {
				case msg.PartText:
					if p.Text != "" {
						text = append(text, p.Text)
					}
				case msg.PartToolCall:
					tc := ToolCall{ID: p.ToolCallID, Type: "function"}
					tc.Function.Name = p.ToolName
					tc.Function.Arguments = string(p.Input)
					if tc.Function.Arguments == "" {
						tc.Function.Arguments = "{}"
					}
					am.ToolCalls = append(am.ToolCalls, tc)
				}
			}
			if len(text) > 0 {
				am.Content = str(strings.Join(text, "\n"))
			}
			if am.Content == nil && len(am.ToolCalls) == 0 {
				am.Content = str("")
			}
			req.Messages = append(req.Messages, am)
		default:
			var text []string
			for _, p := range m.Parts {
				if p.Type == msg.PartText && p.Text != "" {
					text = append(text, p.Text)
				}
			}
			if len(text) == 0 {
				continue
			}
			req.Messages = append(req.Messages, Message{Role: "user", Content: str(strings.Join(text, "\n\n"))})
		}
	}
	return req, validate(req)
}

func validate(r Request) error {
	open := map[string]bool{}
	for i, m := range r.Messages {
		switch m.Role {
		case "tool":
			if !open[m.ToolCallID] {
				return fmt.Errorf("%w: tool message %d (%s) has no matching tool call", ErrInvalid, i, m.ToolCallID)
			}
			delete(open, m.ToolCallID)
		case "assistant":
			if len(open) > 0 {
				return fmt.Errorf("%w: unanswered tool calls before message %d", ErrInvalid, i)
			}
			for _, tc := range m.ToolCalls {
				open[tc.ID] = true
			}
		default:
			if len(open) > 0 {
				return fmt.Errorf("%w: unanswered tool calls before message %d", ErrInvalid, i)
			}
		}
	}
	return nil
}

type Response struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content          *string    `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

func Parse(r Response) (msg.Message, msg.Usage, error) {
	if len(r.Choices) == 0 {
		return msg.Message{}, msg.Usage{}, errors.New("openai: response has no choices")
	}
	ch := r.Choices[0].Message
	m := msg.Message{Role: msg.RoleAssistant}
	if ch.Content != nil && *ch.Content != "" {
		m.Parts = append(m.Parts, msg.Part{Type: msg.PartText, Text: *ch.Content})
	}
	for _, tc := range ch.ToolCalls {
		in := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(in) {
			b, _ := json.Marshal(map[string]string{"_raw_arguments": tc.Function.Arguments})
			in = b
		}
		m.Parts = append(m.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: tc.ID, ToolName: tc.Function.Name, Input: in})
	}
	if len(m.Parts) == 0 {
		m.Parts = []msg.Part{{Type: msg.PartText, Text: ""}}
	}
	cached := r.Usage.PromptTokensDetails.CachedTokens
	u := msg.Usage{InputTokens: r.Usage.PromptTokens - cached, OutputTokens: r.Usage.CompletionTokens, CacheReadTokens: cached}
	return m, u, nil
}

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	Options Options
}

func NewClient(baseURL, model string) *Client {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	key := os.Getenv("OPENAI_API_KEY")
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: key, HTTP: &http.Client{Timeout: 10 * time.Minute}, Options: Options{Model: model}}
}

type APIError struct {
	Status     int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string { return fmt.Sprintf("openai: HTTP %d: %s", e.Status, e.Body) }

func (c *Client) Send(ctx context.Context, req Request) (Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	hr, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	hr.Header.Set("content-type", "application/json")
	if c.APIKey != "" {
		hr.Header.Set("authorization", "Bearer "+c.APIKey)
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
	return Parse(resp)
}

func (c *Client) Complete(ctx context.Context, system, user string, maxTokens int) (summarize.Completion, error) {
	zero := 0.0
	req := Request{Model: c.Options.Model, MaxTokens: maxTokens, Temperature: &zero, Seed: c.Options.Seed, ResponseFormat: json.RawMessage(`{"type":"json_object"}`),
		Messages: []Message{{Role: "system", Content: str(system)}, {Role: "user", Content: str(user)}}}
	resp, err := c.Send(ctx, req)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 429 {
			return summarize.Completion{}, &engine.RateLimitError{RetryAfterMS: ae.RetryAfter.Milliseconds()}
		}
		return summarize.Completion{}, err
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == nil {
		return summarize.Completion{}, errors.New("openai: empty completion")
	}
	return summarize.Completion{Text: *resp.Choices[0].Message.Content, Model: resp.Model, InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens}, nil
}
