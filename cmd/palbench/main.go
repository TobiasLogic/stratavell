package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/adapters/ollama"
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
	reader := flag.String("reader-model", "", "Ollama model used as a reader for the end-to-end question test")
	readerPoints := flag.Int("reader-points", 3, "probe points per seed for the reader test")
	ollamaURL := flag.String("ollama", "http://127.0.0.1:11434", "Ollama base URL")
	ollamaSum := flag.String("ollama-summarizer", "", "Ollama model used as the summarizer (native API, num_ctx set explicitly)")
	orSum := flag.String("openrouter-summarizer", "", "OpenRouter model id used as the summarizer")
	orReader := flag.String("openrouter-reader", "", "OpenRouter model id used as the reader for the end-to-end question test")
	regrade := flag.String("regrade", "", "comma-separated reader.jsonl files to grade again with the current answer keys")
	keyFile := flag.String("openrouter-key-file", filepath.Join(os.Getenv("HOME"), ".config", "palimpsest", "openrouter.env"), "file holding OPENROUTER_API_KEY=... (or just the key)")
	flag.Parse()
	if *regrade != "" {
		var all []bench.ReaderRecord
		for _, path := range strings.Split(*regrade, ",") {
			data, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var r bench.ReaderRecord
				if json.Unmarshal([]byte(line), &r) == nil {
					all = append(all, r)
				}
			}
		}
		re := bench.Regrade(all, *turns)
		if err := os.MkdirAll(*out, 0o755); err == nil {
			f, _ := os.Create(filepath.Join(*out, "reader.regraded.jsonl"))
			enc := json.NewEncoder(f)
			for _, r := range re {
				enc.Encode(r)
			}
			f.Close()
		}
		rep := "## " + *title + "\n\n" + bench.ReaderReport(re, *reader)
		os.WriteFile(filepath.Join(*out, "reader.md"), []byte(rep), 0o644)
		fmt.Print(rep)
		return
	}
	orKey := func() string {
		if k := os.Getenv("OPENROUTER_API_KEY"); k != "" {
			return k
		}
		b, err := os.ReadFile(*keyFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "palbench: no OpenRouter key: set OPENROUTER_API_KEY or create", *keyFile)
			os.Exit(2)
		}
		k := strings.TrimSpace(string(b))
		if i := strings.Index(k, "="); i >= 0 {
			k = strings.Trim(strings.TrimSpace(k[i+1:]), `"'`)
		}
		return k
	}
	orClient := func(model string) *openai.Client {
		cl := openai.NewClient("https://openrouter.ai/api/v1", model)
		cl.APIKey = orKey()
		cl.NoJSON = strings.HasPrefix(model, "anthropic/")
		return cl
	}
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
	if *ollamaSum != "" {
		cl := ollama.New(*ollamaURL, *ollamaSum, 16384)
		cfg.Summarizer = summarize.LLM{Model: cl, MaxInput: 10000}
		cfg.SumName = "Ollama " + *ollamaSum + " (num_ctx 16384, temperature 0)"
		cfg.RealTiming = true
	}
	if *orSum != "" {
		cfg.Summarizer = summarize.LLM{Model: retrying{orClient(*orSum)}, MaxInput: 16000}
		cfg.SumName = "OpenRouter " + *orSum + " (temperature 0)"
		cfg.RealTiming = true
	}
	var _ engine.Summarizer = cfg.Summarizer
	var asker bench.Asker
	readerName := ""
	if *reader != "" {
		asker = bench.OllamaAsker{C: ollama.New(*ollamaURL, *reader, 32768)}
		readerName = *reader + " via Ollama, num_ctx 32768, temperature 0, seed 7"
	}
	if *orReader != "" {
		asker = bench.OpenAIAsker{C: orClient(*orReader)}
		readerName = *orReader + " via OpenRouter, temperature 0"
	}
	if asker != nil {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		f, err := os.Create(filepath.Join(*out, "reader.jsonl"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		recs, err := cfg.RunReader(context.Background(), asker, *readerPoints, f)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, "palbench:", err)
			os.Exit(1)
		}
		rep := "## " + *title + "\n\n" + bench.ReaderReport(recs, readerName)
		os.WriteFile(filepath.Join(*out, "reader.md"), []byte(rep), 0o644)
		fmt.Print(rep)
		return
	}
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

type retrying struct{ c *openai.Client }

func (r retrying) Complete(ctx context.Context, system, user string, maxTokens int) (summarize.Completion, error) {
	var out summarize.Completion
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		out, err = r.c.Complete(ctx, system, user, maxTokens)
		if err == nil {
			return out, nil
		}
		if ctx.Err() != nil {
			return out, err
		}
		time.Sleep(time.Duration(2<<attempt) * time.Second)
	}
	return out, err
}
