package bench

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TobiasLogic/stratavell/summarize"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/smoke-golden.json from the current build")

type smokeRow struct {
	Strategy      string            `json:"strategy"`
	RawTokens     int               `json:"raw_session_tokens"`
	InputTokens   int64             `json:"input_tokens"`
	CacheRead     int64             `json:"cache_read_tokens"`
	CacheWrite    int64             `json:"cache_write_tokens"`
	Uncached      int64             `json:"uncached_tokens"`
	ExtraCalls    int               `json:"extra_primary_calls"`
	ExtraOutput   int64             `json:"extra_primary_output_tokens"`
	ToolTokens    int64             `json:"tool_tokens_sent"`
	MaxContext    int               `json:"max_context"`
	Overflows     int               `json:"overflow_turns"`
	Probes        int               `json:"probes"`
	ByKind        map[string][2]int `json:"probe_by_kind"`
	InputCost     string            `json:"input_cost_usd"`
	TotalCost     string            `json:"total_cost_usd"`
	Completion    string            `json:"completion_rate"`
	Grounded      string            `json:"probe_grounded"`
	Recoverable   string            `json:"probe_recoverable"`
	Standing      string            `json:"standing_retention"`
	ExactRecovery string            `json:"exact_recovery"`
}

func f4(x float64) string { return fmt.Sprintf("%.4f", x) }

func TestDeterministicSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("benchmark smoke run skipped in -short mode")
	}
	cfg := Config{
		Window:      32000,
		MaxOutput:   4000,
		Turns:       120,
		Seeds:       []uint64{1},
		Strategies:  []string{"B-conventional", "C-noa", "F-async-progressive", "F-async-progressive+retrieval", "G-hybrid"},
		OutDir:      t.TempDir(),
		Summarizer:  summarize.Extractive{},
		SumName:     "extractive",
		TurnSeconds: 6,
		Prices:      DefaultPrices(),
		Latency:     DefaultLatency(),
	}
	runs, err := cfg.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []smokeRow
	for _, r := range runs {
		got = append(got, smokeRow{
			Strategy: r.Strategy, RawTokens: r.RawTokens, InputTokens: r.InputTokens,
			CacheRead: r.CacheReadTokens, CacheWrite: r.CacheWriteTokens, Uncached: r.UncachedTokens,
			ExtraCalls: r.ExtraCalls, ExtraOutput: r.ExtraOutput, ToolTokens: r.ToolTokens,
			MaxContext: r.MaxContext, Overflows: r.Overflows, Probes: r.Probes, ByKind: r.ByKind,
			InputCost: f4(r.CostUSD), TotalCost: f4(r.TotalCostUSD), Completion: f4(r.CompletionRate),
			Grounded: f4(r.ProbeGrounded), Recoverable: f4(r.ProbeRecoverable), Standing: f4(r.StandingRetention),
			ExactRecovery: f4(r.ExactRecovery),
		})
	}
	path := filepath.Join("testdata", "smoke-golden.json")
	if *updateGolden {
		b, _ := json.MarshalIndent(got, "", "  ")
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update-golden to create it)", err)
	}
	var want []smokeRow
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if len(want) != len(got) {
		t.Fatalf("golden has %d rows, run produced %d", len(want), len(got))
	}
	var diffs []string
	for i := range got {
		g, _ := json.Marshal(got[i])
		w, _ := json.Marshal(want[i])
		if string(g) != string(w) {
			diffs = append(diffs, fmt.Sprintf("%s\n  want %s\n  got  %s", got[i].Strategy, w, g))
		}
	}
	if len(diffs) > 0 {
		t.Fatalf("deterministic benchmark output changed; if the change is intended, rerun with -update-golden and document it in CHANGELOG.md\n%s", strings.Join(diffs, "\n"))
	}
}
