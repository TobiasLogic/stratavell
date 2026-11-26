# Reader test v4: frozen protocol

This file is committed and pushed before the run. Nothing listed here changes after the first question is sent. A later change becomes a new protocol version, and this run is reported as it came out.

## Question

Does the file-provenance fix from v0.1.0 (file paths in search hits, event pages and injected evidence) remove the hedged answers that v3 found? And does the parity between Stratavell and the component-matched baselines hold across three model families instead of one?

## What changed from v3

- **Readers:** three free models from three different labs replace Claude Haiku 5.5, so no API credit is spent.
- **Output cap:** 1,500 tokens instead of 600, because some free models reason before answering. In a pilot, one answer used 456 tokens.
- **Build:** includes the provenance fix (in since v0.1.0) and the v0.1.1 search changes.
- **Rate limits:** requests are paced and retried, because free endpoints are rate-limited.

Everything else is the same as v3: workload, strategies, probe points, retrieval policy, question prompt and grader.

## Fixed setup

- **Build:** the commit that adds this file. The workload, grader, keys and prompt are taken from that commit.
- **Workload:** `bench.Generate(seed=1, turns=300)`, a 64k window with 8k reserved for output, and the extractive summarizer for all strategies.
- **Strategies,** exactly three, as in v3:
  - `G-hybrid`
  - `F-async-progressive+retrieval`
  - `B-conventional+archive`
- **Probe points:** 4, chosen by `RunReader`. Every fact known at each point is asked, about 33 questions per strategy.
- **Retrieval:** when the fact and its evidence keys are not both visible in the assembled context, the fixed retrieval policy runs and its text is appended before the question. The policy is `memory_search` with the fact's stored query, then the top two hits read in full, with a 3k-token budget.
- **Readers,** via OpenRouter, each run on all three strategies:
  - `nvidia/nemotron-3-ultra-550b-a55b:free` (NVIDIA)
  - `baidu/ernie-5.5-flash-preview:free` (Baidu)
  - `inclusionai/ling-3.2-flash:free` (inclusionAI)
- **Reader settings:** temperature 0, at most 1,500 output tokens, the prompt as in `bench/reader.go` at this commit, at least 3.5 seconds between requests, and up to 10 attempts on an API error, with waits capped at one minute.
- **Selection:** these three were chosen in a pilot of four free models on a 120-turn session. `google/gemma-4-31b-it:free` was dropped because every request returned an upstream 429. The pilot answers are not results and are not reported as such.
- **Model versions:** free endpoints can change behind the same id. The run date is recorded, and the results apply to the models as served on that date.
- **Budget:** free models only, so the run costs $0. About 300 questions plus retries, within the account's free daily limit.

## Grading, three dimensions, as in v3

1. **value recovered** (lenient): the answer contains the key, per `grade()`.
2. **evidence available:** the context, or the appended retrieval text, contains both the key and the evidence keys.
3. **grounded answer** (strict, the headline): the value is recovered and the answer does not hedge, per `Hedged()`.

## Unanswered questions

A question whose request still fails after all attempts is **unanswered**:
- It is reported separately and is not counted as a wrong answer.
- The three grades for that model and strategy are given over the answered questions, with the unanswered count next to them.
- If more than 10% of a model's questions are unanswered, that model's results are reported as incomplete.

## Expectations stated before the run

- **E1:** with the provenance fix, hedges on the two config-value questions (`value_retry`, `value_port`) are at most 1 per strategy for each reader. In v3, with Haiku, they were the main source of strict misses.
- **E2:** across all three readers, no strategy's strict grounded count differs from G's by more than 3, which is parity as in v3.

Each expectation is reported as met or not met, per reader.

## Reporting

All three grades are reported for all three strategies and all three readers, with the unanswered counts and the run date. The comparison with v3 is descriptive only, because the reader models differ. The run is published whatever the outcome, including failed or partial runs.
