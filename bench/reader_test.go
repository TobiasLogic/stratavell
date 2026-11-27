package bench

import (
	"context"
	"io"
	"testing"

	"github.com/TobiasLogic/stratavell/summarize"
)

func TestRedactAccount(t *testing.T) {
	in := `HTTP 429: {"error":{"message":"Provider returned error","code":429},"user_id":"user_2abcDEF123"}`
	want := `HTTP 429: {"error":{"message":"Provider returned error","code":429},"user_id":"redacted"}`
	if got := redactAccount(in); got != want {
		t.Fatalf("got %s", got)
	}
	if got := redactAccount("plain error"); got != "plain error" {
		t.Fatalf("changed text without an account id: %s", got)
	}
}

func readerConfig(t *testing.T, turns int, strategies ...string) Config {
	return Config{Window: 64000, MaxOutput: 8000, Turns: turns, OutDir: t.TempDir(), Summarizer: summarize.Extractive{}, SumName: "extractive", TurnSeconds: 6, Prices: DefaultPrices(), Latency: DefaultLatency(), Seeds: []uint64{1}, Strategies: strategies}
}

func TestReaderOnlyAsksSelectedQuestions(t *testing.T) {
	c := readerConfig(t, 120, "G-hybrid")
	all, err := c.RunReader(context.Background(), DryAsker{}, 1, io.Discard)
	if err != nil || len(all) < 2 {
		t.Fatalf("dry run: %d records, %v", len(all), err)
	}
	pick := ReaderSlot(all[1].Strategy, all[1].Turn, all[1].Fact)
	c.ReaderOnly = map[string]bool{pick: true}
	c.OutDir = t.TempDir()
	got, err := c.RunReader(context.Background(), DryAsker{}, 1, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || ReaderSlot(got[0].Strategy, got[0].Turn, got[0].Fact) != pick {
		t.Fatalf("want only %s, got %+v", pick, got)
	}
	if got[0].Visible != all[1].Visible || got[0].Evidence != all[1].Evidence {
		t.Fatalf("filtered record differs from the full run: %+v vs %+v", got[0], all[1])
	}
}

func TestReaderStandingRuleNotCreditedFromCode(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a 300-turn session")
	}
	c := readerConfig(t, 300, "F-async-progressive+retrieval")
	c.ReaderOnly = map[string]bool{ReaderSlot("F-async-progressive+retrieval", 151, "constraint_api"): true}
	got, err := c.RunReader(context.Background(), DryAsker{}, 4, io.Discard)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d records, %v", len(got), err)
	}
	r := got[0]
	if r.Visible || r.Grounded || !r.Retrieved || !r.Evidence {
		t.Fatalf("the rule sentence is not in F's context at turn 151, so it must not count as visible and retrieval must supply it: %+v", r)
	}
}
