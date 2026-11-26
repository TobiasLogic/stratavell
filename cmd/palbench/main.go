package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/adapters/openai"
	"github.com/TobiasLogic/palimpsest/bench"
	"github.com/TobiasLogic/palimpsest/engine"
	"github.com/TobiasLogic/palimpsest/summarize"
)

func main() {
	window := flag.Int("window", 64000, "context window in tokens")
	maxOut := flag.Int("max-output", 8000, "reserved output tokens")
	turns := flag.Int("turns", 300, "turns per session")
	seeds := flag.String("seeds", "1,2,3", "comma-separated seeds")
	strategies := flag.String("strategies", "main", "main, ablation, all, or a comma-separated list")
	out := flag.String("out", "bench/out", "output directory")
	sumURL := flag.String("summarizer-url", "", "OpenAI-compatible endpoint for a real summarizer (default: extractive, no model)")
	sumModel := flag.String("summarizer-model", "", "model name for the real summarizer")
	turnSec := flag.Float64("turn-seconds", 6, "simulated wall time of one agent turn")
	title := flag.String("title", "", "report title")
	flag.Parse()
	cfg := bench.Config{Window: *window, MaxOutput: *maxOut, Turns: *turns, OutDir: *out, Summarizer: summarize.Extractive{}, SumName: "extractive (deterministic, no model)", TurnSeconds: *turnSec, Prices: bench.DefaultPrices(), Latency: bench.DefaultLatency()}
	cfg.Log = func(f string, a ...any) { fmt.Fprintf(os.Stderr, f+"\n", a...) }
	for _, s := range strings.Split(*seeds, ",") {
		v, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
		if err != nil {
			fmt.Fprintln(os.Stderr, "bad seed", s)
			os.Exit(2)
		}
		cfg.Seeds = append(cfg.Seeds, v)
	}
	switch *strategies {
	case "main":
		cfg.Strategies = bench.AllStrategies
	case "ablation":
		cfg.Strategies = append([]string{"G-hybrid"}, bench.Ablations...)
	case "all":
		cfg.Strategies = append(append([]string{}, bench.AllStrategies...), bench.Ablations...)
	default:
		cfg.Strategies = strings.Split(*strategies, ",")
	}
	if *sumURL != "" {
		cl := openai.NewClient(*sumURL, *sumModel)
		seed := 7
		cl.Options.Seed = &seed
		cfg.Summarizer = summarize.LLM{Model: cl, MaxInput: 12000}
		cfg.SumName = "LLM " + *sumModel + " via " + *sumURL
		cfg.RealTiming = true
	}
	var _ engine.Summarizer = cfg.Summarizer
	start := time.Now()
	runs, err := cfg.Run(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "palbench:", err)
		os.Exit(1)
	}
	t := *title
	if t == "" {
		t = fmt.Sprintf("Run %s", time.Now().UTC().Format("2006-01-02 15:04 UTC"))
	}
	rep := bench.Report(cfg, runs, t)
	rep += fmt.Sprintf("\nWall time %.1f s.\n", time.Since(start).Seconds())
	if err := os.WriteFile(filepath.Join(*out, "report.md"), []byte(rep), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	fmt.Print(rep)
}
