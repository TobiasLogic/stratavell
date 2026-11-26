# Reader test v3: frozen protocol

This file is committed before the run. Nothing listed here changes after the first question is sent. Any later change becomes a new protocol version, and this run is reported as it came out.

## Question

When strategies get comparable retrieval access, does palimpsest still answer questions about earlier work at least as well? This responds to review question 3 (component-matched comparison) and issue D (evidence, not just the name).

## Fixed setup

- Build: the commit that adds this file. The workload, grader, keys and prompt are taken from that commit.
- Workload: `bench.Generate(seed=1, turns=300)`, 64k window, 8k reserved output, extractive summarizer for all strategies.
- Strategies, exactly three:
  - `G-hybrid`
  - `F-async-progressive+retrieval`: APEC-style background summaries plus the same memory tools and auto-retrieval
  - `B-conventional+archive`: conventional compaction proxy plus a passive palimpsest archive with retrieval, like the MCP passive mode
- Probe points: 4, chosen by `RunReader` as before. Every fact known at that point is asked, about 32 questions per strategy.
- Retrieval: for every strategy, when the fact and its evidence keys are not both visible in the assembled context, the fixed retrieval policy runs and its text is appended before the question. The policy is `memory_search` with the fact's stored query, then the top two hits expanded, 3k-token budget.
- Reader: `anthropic/claude-haiku-5.5` via OpenRouter, temperature 0, 600 output tokens, prompt as in `bench/reader.go` at this commit.
- Budget: stop if OpenRouter spend passes $4.90.

## Grading, three dimensions

1. **value recovered** (lenient): the answer contains the key. The rules are those in `grade()`.
2. **evidence available**: the context, or the appended retrieval text, contains both the key and the evidence keys. Only the fsync-batching symbol fact has separate evidence (`w.fsyncGroup(ctx, frames)`); for the others the key is its own evidence.
3. **grounded answer** (strict, the headline): value recovered, and the answer does not hedge. Hedging means "I don't know", "not shown", "not stated", "cannot confirm" and similar phrases, per `Hedged()`.

## Reporting

All three dimensions are reported for all three strategies. The run is published whatever the outcome, including failed or partial runs.
