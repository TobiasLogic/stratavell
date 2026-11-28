package engine

import (
	"context"
	"errors"
)

type SummaryInputItem struct {
	ID   string   `json:"id"`
	Seq  int64    `json:"seq"`
	Role string   `json:"role"`
	Tool string   `json:"tool,omitempty"`
	Text string   `json:"text"`
	Lo   int64    `json:"lo,omitempty"`
	Hi   int64    `json:"hi,omitempty"`
	Refs []string `json:"refs,omitempty"`
}

type SummarizeInput struct {
	SessionID     string
	AgentID       string
	Level         int
	Items         []SummaryInputItem
	Prior         string
	MaxTokens     int
	PromptVersion string
	Strict        bool
	JobID         string
	Attempt       int
}

type SummarizeOutput struct {
	Text         string
	Facts        []Fact
	Model        string
	InputTokens  int
	OutputTokens int
}

type Summarizer interface {
	Summarize(ctx context.Context, in SummarizeInput) (SummarizeOutput, error)
}

var ErrRateLimited = errors.New("summarizer: rate limited")

type RateLimitError struct {
	RetryAfterMS int64
}

func (e *RateLimitError) Error() string   { return "summarizer: rate limited" }
func (e *RateLimitError) Is(t error) bool { return t == ErrRateLimited }
