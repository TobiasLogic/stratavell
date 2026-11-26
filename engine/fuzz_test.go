package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/TobiasLogic/stratavell/engine"
	"github.com/TobiasLogic/stratavell/msg"
)

func FuzzSearchQuery(f *testing.F) {
	for _, s := range []string{"", "a OR b", `"unterminated`, "NEAR(x y)", "col:value", "*", "^start", "ev_01ARZ3NDEKTSV4RRFFQ69G5FAV", "pkg/a.go", "x AND NOT y", "(((", "日本語 テスト", "a\x00b"} {
		f.Add(s)
	}
	e := openEngine(f, smallPolicy(16000))
	a := mustAgent(f, e, "s", "a")
	mustAppend(f, a, msg.Text(msg.RoleUser, "alpha beta gamma pkg/a.go handleRequest NEAR the column"))
	f.Fuzz(func(t *testing.T, q string) {
		if _, err := a.Search(context.Background(), q, engine.SearchFilters{}, 5); err != nil {
			t.Fatalf("query %q: %v", q, err)
		}
	})
}

func FuzzAppendAssemble(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 200, 3, 9, 0, 0, 255})
	f.Add([]byte("the quick brown fox jumps over the lazy dog many times over"))
	f.Fuzz(func(t *testing.T, ops []byte) {
		if len(ops) > 300 {
			ops = ops[:300]
		}
		pol := smallPolicy(9000)
		e := openEngine(t, pol, manual())
		a := mustAgent(t, e, "s", "a")
		ctx := context.Background()
		open := []string{}
		n := 0
		for i, op := range ops {
			switch {
			case len(open) > 0:
				id := open[0]
				open = open[1:]
				size := int(op) * int(op) / 4
				text := strings.Repeat(fmt.Sprintf("line %d value_%d\n", i, op), size/16+1)
				code := int(op % 3)
				if _, err := a.Append(ctx, msg.ToolResult(id, "bash", text, code != 0, msg.Meta{ExitCode: &code, Command: "go test ./..."})); err != nil {
					t.Fatal(err)
				}
			case op%4 == 0:
				if _, err := a.Append(ctx, msg.Text(msg.RoleUser, fmt.Sprintf("user %d: never break rule %d", i, op))); err != nil {
					t.Fatal(err)
				}
			default:
				c, err := a.Assemble(ctx, engine.AssembleRequest{IncludeMemoryTools: op%2 == 0})
				if err != nil {
					t.Fatalf("assemble: %v", err)
				}
				if c.Tokens > c.Capacity {
					t.Fatalf("overflow %d > %d", c.Tokens, c.Capacity)
				}
				calls := int(op%3) + 1
				m := msg.Message{Role: msg.RoleAssistant}
				for k := 0; k < calls; k++ {
					n++
					id := fmt.Sprintf("c%d", n)
					in, _ := json.Marshal(map[string]string{"command": "go test ./...", "content": strings.Repeat("x", int(op)*8)})
					m.Parts = append(m.Parts, msg.Part{Type: msg.PartToolCall, ToolCallID: id, ToolName: "bash", Input: in})
					open = append(open, id)
				}
				if _, err := a.Append(ctx, m); err != nil {
					t.Fatal(err)
				}
				if op%5 == 0 {
					e.RunJobs(ctx)
				}
			}
		}
		rep, err := e.Verify(ctx, "s")
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Problems) > 0 {
			t.Fatal(rep.Problems)
		}
	})
}
