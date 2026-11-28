package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TobiasLogic/stratavell/adapters/openai"
	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
	"github.com/TobiasLogic/stratavell/summarize"
)

type Client struct {
	BaseURL     string
	Model       string
	NumCtx      int
	Seed        int
	Temperature float64
	KeepAlive   string
	HTTP        *http.Client
}

func New(baseURL, model string, numCtx int) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:11434"
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Model: model, NumCtx: numCtx, Seed: 7, KeepAlive: "30m", HTTP: &http.Client{Timeout: 20 * time.Minute}}
}

type toolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type chatRequest struct {
	Model     string         `json:"model"`
	Messages  []message      `json:"messages"`
	Stream    bool           `json:"stream"`
	Format    string         `json:"format,omitempty"`
	Options   map[string]any `json:"options"`
	KeepAlive string         `json:"keep_alive,omitempty"`
}

type chatResponse struct {
	Model           string  `json:"model"`
	Message         message `json:"message"`
	PromptEvalCount int     `json:"prompt_eval_count"`
	EvalCount       int     `json:"eval_count"`
	TotalDuration   int64   `json:"total_duration"`
	Error           string  `json:"error"`
}

type Result struct {
	Text         string
	InputTokens  int
	OutputTokens int
	Duration     time.Duration
}

func (c *Client) Chat(ctx context.Context, msgs []message, format string, maxTokens int) (Result, error) {
	opts := map[string]any{"num_ctx": c.NumCtx, "seed": c.Seed, "temperature": c.Temperature}
	if maxTokens > 0 {
		opts["num_predict"] = maxTokens
	}
	body, _ := json.Marshal(chatRequest{Model: c.Model, Messages: msgs, Stream: false, Format: format, Options: opts, KeepAlive: c.KeepAlive})
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("content-type", "application/json")
	engine.TagRequest(ctx, req.Header)
	start := time.Now()
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return Result{}, err
	}
	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return Result{}, fmt.Errorf("ollama: %s", strings.TrimSpace(string(data)))
	}
	if resp.StatusCode/100 != 2 || out.Error != "" {
		return Result{}, fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, out.Error)
	}
	return Result{Text: out.Message.Content, InputTokens: out.PromptEvalCount, OutputTokens: out.EvalCount, Duration: time.Since(start)}, nil
}

func (c *Client) Complete(ctx context.Context, system, user string, maxTokens int) (summarize.Completion, error) {
	r, err := c.Chat(ctx, []message{{Role: "system", Content: system}, {Role: "user", Content: user}}, "json", maxTokens)
	if err != nil {
		return summarize.Completion{}, err
	}
	return summarize.Completion{Text: r.Text, Model: "ollama/" + c.Model, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}, nil
}

func Messages(cx *engine.Context) ([]message, error) {
	req, err := openai.Render(cx, openai.Options{Model: "x"})
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	var out []message
	for _, m := range req.Messages {
		om := message{Role: m.Role}
		if m.Content != nil {
			om.Content = *m.Content
		}
		for _, tc := range m.ToolCalls {
			var t toolCall
			t.Function.Name = tc.Function.Name
			args := json.RawMessage(tc.Function.Arguments)
			if !json.Valid(args) {
				args = json.RawMessage(`{}`)
			}
			t.Function.Arguments = args
			om.ToolCalls = append(om.ToolCalls, t)
			names[tc.ID] = tc.Function.Name
		}
		if m.Role == "tool" {
			om.ToolName = names[m.ToolCallID]
		}
		out = append(out, om)
	}
	return out, nil
}

func (c *Client) Ask(ctx context.Context, cx *engine.Context, extra []string, question string, maxTokens int) (Result, error) {
	msgs, err := Messages(cx)
	if err != nil {
		return Result{}, err
	}
	for _, e := range extra {
		msgs = append(msgs, message{Role: "user", Content: e})
	}
	msgs = append(msgs, message{Role: "user", Content: question})
	return c.Chat(ctx, msgs, "", maxTokens)
}

func (c *Client) Generate(ctx context.Context, cx *engine.Context) (msg.Message, msg.Usage, error) {
	msgs, err := Messages(cx)
	if err != nil {
		return msg.Message{}, msg.Usage{}, err
	}
	r, err := c.Chat(ctx, msgs, "", 0)
	if err != nil {
		return msg.Message{}, msg.Usage{}, err
	}
	return msg.Text(msg.RoleAssistant, r.Text), msg.Usage{InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}, nil
}
