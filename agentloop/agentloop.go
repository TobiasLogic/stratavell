package agentloop

import (
	"context"
	"errors"
	"fmt"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
)

type Model interface {
	Generate(ctx context.Context, c *engine.Context) (msg.Message, msg.Usage, error)
}

type ToolRunner interface {
	Run(ctx context.Context, call msg.Part) (msg.Message, error)
}

type ToolFunc func(ctx context.Context, call msg.Part) (msg.Message, error)

func (f ToolFunc) Run(ctx context.Context, call msg.Part) (msg.Message, error) { return f(ctx, call) }

type Turn struct {
	Assistant msg.Message
	Results   []msg.Message
	Context   *engine.Context
	Usage     msg.Usage
	Done      bool
}

func Step(ctx context.Context, a *engine.Agent, m Model, tools ToolRunner, req engine.AssembleRequest) (Turn, error) {
	cx, err := a.Assemble(ctx, req)
	if err != nil {
		return Turn{}, err
	}
	out, usage, err := m.Generate(ctx, cx)
	if err != nil {
		return Turn{Context: cx}, err
	}
	a.ObserveUsage(usage)
	if _, err := a.Append(ctx, out); err != nil {
		return Turn{Context: cx}, fmt.Errorf("agentloop: append assistant: %w", err)
	}
	t := Turn{Assistant: out, Context: cx, Usage: usage}
	calls := out.ToolCalls()
	if len(calls) == 0 {
		t.Done = true
		return t, nil
	}
	for _, c := range calls {
		var res msg.Message
		if engine.IsMemoryTool(c.ToolName) {
			o := a.HandleTool(ctx, c.ToolName, c.Input)
			res = msg.ToolResult(c.ToolCallID, c.ToolName, o.Text, o.IsError, msg.Meta{ContentType: msg.CTRetrieval})
		} else if tools == nil {
			res = msg.ToolResult(c.ToolCallID, c.ToolName, "no tool runner configured", true, msg.Meta{})
		} else {
			r, err := tools.Run(ctx, c)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					r = msg.ToolResult(c.ToolCallID, c.ToolName, "tool call cancelled", true, msg.Meta{})
				} else {
					r = msg.ToolResult(c.ToolCallID, c.ToolName, "tool error: "+err.Error(), true, msg.Meta{})
				}
			}
			res = r
		}
		if p, ok := res.ToolResultPart(); !ok || p.ToolCallID != c.ToolCallID {
			res = msg.ToolResult(c.ToolCallID, c.ToolName, "tool runner returned a result for the wrong call", true, msg.Meta{})
		}
		if _, err := a.Append(ctx, res); err != nil {
			return t, fmt.Errorf("agentloop: append tool result: %w", err)
		}
		t.Results = append(t.Results, res)
	}
	return t, nil
}

func Run(ctx context.Context, a *engine.Agent, m Model, tools ToolRunner, req engine.AssembleRequest, maxSteps int) ([]Turn, error) {
	var turns []Turn
	for i := 0; maxSteps <= 0 || i < maxSteps; i++ {
		t, err := Step(ctx, a, m, tools, req)
		turns = append(turns, t)
		if err != nil || t.Done {
			return turns, err
		}
	}
	return turns, nil
}
