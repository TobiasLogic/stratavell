# Reader test v4.2: frozen protocol

v4.2 asks one question from v4 and v4.1 again, with the corrected standing-rule keys. This file is committed before any reader gets that question.

## Why

On 2026-11-27 the three standing-rule keys were found to match ordinary session text, and each rule is now keyed on its own sentence (635fcb5). The reader test uses the same check to decide whether to append retrieval, and v4 and v4.1 ran before the fix.

A dry run at this commit (`-reader-dry-run`, no model calls) records what every v4 question would see with the corrected keys. It is in `bench/results/2026-11-27/reader-v4-dry-run/`. Compared with the stored v4 and v4.1 records, exactly one of the 99 question slots changes, and it changes for every reader:
- **The slot:** `F-async-progressive+retrieval@151:constraint_api`, "Which package's public API must stay unchanged?".
- **Before:** the old key `package store` matched the first line of a Go file, so the rule counted as visible and retrieval was skipped.
- **Now:** the rule sentence is not in F's context, so retrieval is appended.
- **The other 98 slots:** visible, grounded, evidence and retrieval fields are identical, so those prompts are unchanged.

All three readers in v4 and v4.1, and Haiku in v3, missed this question.

## What is decided after results were seen

All of v4 and v4.1 were known when this was written. To keep the rerun from being a second draw on questions whose prompts did not change, it is limited to the one slot whose input changed.

## Run

- **Build:** this commit.
- **Workload and settings:** as in v4: `bench.Generate(seed=1, turns=300)`, a 64k window with 8k reserved for output, the extractive summarizer, 4 probe points.
- **Strategy:** only `F-async-progressive+retrieval` is assembled, because only its question is asked. Each strategy has its own state, so this does not change F's context.
- **Readers:** the three that completed v4 and v4.1, with the same settings (temperature 0, at most 1,500 output tokens, at least 3.5 seconds between requests, up to 10 attempts):
  - `nvidia/nemotron-3-ultra-550b-a55b:free`
  - `baidu/ernie-5.5-flash-preview:free`
  - `recursive/flint-v1.0:free`
- **Command,** per reader:

```
stratavell-bench -window 64000 -turns 300 -seeds 1 \
  -strategies F-async-progressive+retrieval -reader-points 4 \
  -reader-only 'F-async-progressive+retrieval@151:constraint_api' \
  -openrouter-reader MODEL -reader-max-tokens 1500 -reader-min-interval 3.5s \
  -out bench/results/2026-11-27/reader-v4.2/MODEL_DIR
```

- **Budget:** three questions on free models, $0.

## Grading and unanswered questions

As in v4: lenient, evidence available and strict, with the same grader. A request that fails after all attempts is unanswered and reported as such.

## Expectation stated before the run

- **E3:** every reader answers the rerun question strictly correctly, as all three did for the same fact with G and B and at F's other probe points.

It is reported as met or not met, per reader.

## Reporting

- The v4 and v4.1 records stay unchanged and are still shown.
- The rerun is reported next to them as a corrected F count for each reader: the original 33 answers with this one slot replaced by its rerun answer.
- The result is published whatever the outcome.
