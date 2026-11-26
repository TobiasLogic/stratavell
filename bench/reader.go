package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/TobiasLogic/palimpsest/adapters/ollama"
	"github.com/TobiasLogic/palimpsest/adapters/openai"
	"github.com/TobiasLogic/palimpsest/engine"
)

type Asker interface {
	Ask(ctx context.Context, cx *engine.Context, extra []string, question string, maxTokens int) (AskResult, error)
}

type AskResult struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

type retrievalTexter interface {
	RetrieveText(ctx context.Context, query string, budget int) string
}

type OllamaAsker struct{ C *ollama.Client }

func (o OllamaAsker) Ask(ctx context.Context, cx *engine.Context, extra []string, q string, n int) (AskResult, error) {
	r, err := o.C.Ask(ctx, cx, extra, q, n)
	return AskResult{Text: r.Text, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}, err
}

type OpenAIAsker struct{ C *openai.Client }

func (o OpenAIAsker) Ask(ctx context.Context, cx *engine.Context, extra []string, q string, n int) (AskResult, error) {
	var r openai.AskResult
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		r, err = o.C.Ask(ctx, cx, extra, q, n)
		if err == nil {
			break
		}
		time.Sleep(time.Duration(2<<attempt) * time.Second)
	}
	return AskResult{Text: r.Text, InputTokens: r.InputTokens, OutputTokens: r.OutputTokens}, err
}

type ReaderRecord struct {
	Strategy  string  `json:"strategy"`
	Seed      uint64  `json:"seed"`
	Turn      int     `json:"turn"`
	Fact      string  `json:"fact"`
	Kind      string  `json:"kind"`
	Visible   bool    `json:"visible"`
	Grounded  bool    `json:"grounded"`
	Retrieved bool    `json:"retrieval_added"`
	Overflow  bool    `json:"overflow"`
	Correct   bool    `json:"correct"`
	Strict    bool    `json:"correct_strict"`
	Evidence  bool    `json:"evidence_available"`
	Answer    string  `json:"answer"`
	PromptTok int     `json:"prompt_tokens"`
	OutTok    int     `json:"output_tokens"`
	Seconds   float64 `json:"seconds"`
}

var reDigits = regexp.MustCompile(`\d+`)

var reHedge = regexp.MustCompile(`(?i)\b(i )?(don't|do not) know\b|not (stated|shown|present|mentioned)|cannot (confirm|determine)|can't (confirm|determine)`)

func Hedged(answer string) bool { return reHedge.MatchString(answer) }

func grade(answer string, keys []string) bool {
	la := strings.ToLower(answer)
	for _, k := range keys {
		lk := strings.ToLower(k)
		if strings.Contains(la, lk) {
			continue
		}
		if len(k) > 40 {
			nums := reDigits.FindAllString(k, -1)
			ok := len(nums) > 0
			for _, n := range nums {
				if !strings.Contains(la, n) {
					ok = false
				}
			}
			if ok {
				continue
			}
		}
		if strings.Contains(lk, " = ") {
			val := strings.TrimSpace(strings.SplitN(lk, " = ", 2)[1])
			if regexp.MustCompile(`(^|[^0-9a-z_])` + regexp.QuoteMeta(val) + `($|[^0-9a-z_])`).MatchString(la) {
				continue
			}
		}
		return false
	}
	return true
}

func (c *Config) RunReader(ctx context.Context, cl Asker, points int, out io.Writer) ([]ReaderRecord, error) {
	enc := json.NewEncoder(out)
	var recs []ReaderRecord
	for _, seed := range c.Seeds {
		sc := Generate(seed, c.Turns)
		var probeSteps []int
		for t, s := range sc.Steps {
			if s.Probe {
				probeSteps = append(probeSteps, t)
			}
		}
		chosen := map[int]bool{}
		for i := 1; i <= points && len(probeSteps) > 0; i++ {
			idx := len(probeSteps) * i / (points + 1)
			chosen[probeSteps[min(idx, len(probeSteps)-1)]+1] = true
		}
		for _, name := range c.Strategies {
			dir := c.OutDir
			start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
			st, err := c.make(ctx, name, dir, seed, start)
			if err != nil {
				return recs, err
			}
			for t, step := range sc.Steps {
				now := start.Add(time.Duration(float64(t) * c.TurnSeconds * float64(time.Second)))
				st.Tick(ctx, now)
				if step.User != nil {
					if err := st.Append(ctx, *step.User); err != nil {
						return recs, err
					}
				}
				v, err := st.Assemble(ctx, now)
				if err != nil {
					return recs, err
				}
				if chosen[t] {
					for _, f := range sc.FactsAt(t - 1) {
						r := ReaderRecord{Strategy: name, Seed: seed, Turn: t, Fact: f.ID, Kind: f.Kind, Overflow: v.Overflow}
						if !v.Overflow {
							r.Visible = containsAll(v.Text, f.Keys)
							r.Grounded = containsAll(v.Text, f.GroundKeys())
							r.Evidence = r.Grounded
							var extra []string
							if rt, ok := st.(retrievalTexter); ok && !r.Grounded {
								if txt := rt.RetrieveText(ctx, f.Query, 3000); txt != "" {
									extra = append(extra, "Results of the memory tools for this question:\n"+txt)
									r.Retrieved = true
									r.Evidence = containsAll(txt, f.GroundKeys())
								}
							}
							q := fmt.Sprintf("Answer this question about the work session above, using only information present in it.\nQuestion: %s\nReply in one short sentence: give the exact value, identifier or phrase, and the reason only if the question asks why. If the session does not contain the answer, say you do not know.", f.Question)
							t0 := time.Now()
							res, err := cl.Ask(ctx, v.Context, extra, q, 600)
							r.Seconds = time.Since(t0).Seconds()
							if err != nil {
								r.Answer = "error: " + err.Error()
							} else {
								r.Answer = strings.TrimSpace(res.Text)
								r.PromptTok = res.InputTokens
								r.OutTok = res.OutputTokens
								r.Correct = grade(r.Answer, f.AnswerKeys())
								r.Strict = r.Correct && !Hedged(r.Answer)
							}
						}
						recs = append(recs, r)
						enc.Encode(r)
						if c.Log != nil {
							c.Log("reader %s t=%d %s visible=%v retr=%v correct=%v %.1fs prompt=%d answer=%q", name, t, f.ID, r.Visible, r.Retrieved, r.Correct, r.Seconds, r.PromptTok, clip(r.Answer, 80))
						}
					}
				}
				if err := st.Append(ctx, step.Assistant); err != nil {
					return recs, err
				}
				for _, res := range step.Results {
					if err := st.Append(ctx, res); err != nil {
						return recs, err
					}
				}
			}
			st.Close()
		}
	}
	return recs, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func ReaderReport(recs []ReaderRecord, model string) string {
	type agg struct{ n, correct, strict, evid, visible, visCorrect, hidden, hidCorrect, ovf int }
	by := map[string]*agg{}
	var order []string
	for _, r := range recs {
		a, ok := by[r.Strategy]
		if !ok {
			a = &agg{}
			by[r.Strategy] = a
			order = append(order, r.Strategy)
		}
		a.n++
		if r.Overflow {
			a.ovf++
		}
		if r.Correct {
			a.correct++
		}
		if r.Strict {
			a.strict++
		}
		if r.Evidence {
			a.evid++
		}
		if r.Visible {
			a.visible++
			if r.Correct {
				a.visCorrect++
			}
		} else if !r.Overflow {
			a.hidden++
			if r.Correct {
				a.hidCorrect++
			}
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Reader model: %s. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.\n\n", model)
	b.WriteString("| strategy | questions | strict correct | strict accuracy % | value recovered (lenient) | evidence available | overflowed | lenient correct when visible | lenient correct when not visible |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, s := range order {
		a := by[s]
		fmt.Fprintf(&b, "| %s | %d | %d | %.1f | %d | %d | %d | %d/%d | %d/%d |\n", s, a.n, a.strict, 100*float64(a.strict)/float64(max(1, a.n)), a.correct, a.evid, a.ovf, a.visCorrect, a.visible, a.hidCorrect, a.hidden)
	}
	return b.String()
}

func (f Fact) AnswerKeys() []string {
	if len(f.Answer) > 0 {
		return f.Answer
	}
	return f.Keys
}

func Regrade(recs []ReaderRecord, turns int) []ReaderRecord {
	facts := map[uint64]map[string]Fact{}
	out := make([]ReaderRecord, len(recs))
	for i, r := range recs {
		fm, ok := facts[r.Seed]
		if !ok {
			fm = map[string]Fact{}
			for _, f := range Generate(r.Seed, turns).Facts {
				fm[f.ID] = f
			}
			facts[r.Seed] = fm
		}
		if f, ok := fm[r.Fact]; ok && !r.Overflow && !strings.HasPrefix(r.Answer, "error:") {
			r.Correct = grade(r.Answer, f.AnswerKeys())
			r.Strict = r.Correct && !Hedged(r.Answer)
		}
		out[i] = r
	}
	return out
}
