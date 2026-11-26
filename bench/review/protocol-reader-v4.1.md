# Reader test v4.1: frozen protocol

v4.1 changes one thing from `protocol-reader-v4.md`: the third reader. Everything else in v4 still applies, unchanged. This file is committed before the replacement reader gets its first question, and before the candidates' pilot results are read.

## Why the change

`inclusionai/ling-3.2-flash:free` was throttled by its upstream provider for the entire v4 run:
- Its first question took 371 seconds and still ended in HTTP 429 after all 10 attempts.
- At that rate, its 99 questions would have taken many hours, with most of them likely unanswered.
- The run was stopped by hand at about 2026-11-26T18:00Z, after 1 question.

The partial Ling run is kept and reported as v4 specified: 1 question asked, 1 unanswered, so its results are **incomplete**. NVIDIA and Baidu finished their v4 runs before this change, with 0 unanswered, and those results stand as they came out.

## What is decided only after their results were seen

The decision to replace the third reader was made after the NVIDIA and Baidu results were known. The replacement only changes which model reads; workload, strategies, probe points, retrieval policy, prompt and grader are the same as in v4. The selection rule below is fixed before the pilot results are read.

## Candidates

Every model OpenRouter listed at $0 on 2026-11-26 that is a text chat model, from a lab not already among the readers, and not already found unusable:
- `google/gemma-4-26b-a4b-it:free` (Google; a different endpoint from `gemma-4-31b-it`, which returned 429 on every pilot request)
- `particle-ai/particle-1:free` (Particle AI)
- `recursive/flint-v1.0:free` (Recursive)
- `liquid/lfm-3-1.7b:free` (Liquid AI)

Excluded: the other NVIDIA endpoints (lab already represented), the Lyria music models, and the `openrouter/free` router, which does not pin one model.

## Pilot

The same pilot as v4:
- 120-turn session, seed 1, `G-hybrid` only, 1 probe point (9 questions)
- reader settings as in v4: 1,500-token cap, 3.5 seconds between requests, up to 10 attempts
- run with the v4 build (`1b0788f`)

Pilot answers are not results and are not reported as such.

## Selection rule

1. A candidate is eligible only if it answers all 9 pilot questions (0 unanswered).
2. Among the eligible candidates, the one with the most strict correct answers is chosen.
3. Ties go to the candidate with the lower mean seconds per question, because the full run is about 99 questions.
4. If no candidate is eligible, v4.1 runs with two readers, and that is reported.

## Run

The chosen reader gets the v4 run exactly:
- 300 turns, seed 1, 64k window
- strategies `G-hybrid`, `F-async-progressive+retrieval` and `B-conventional+archive`
- 4 probe points
- v4 reader settings
- the v4 build (`1b0788f`)

## Expectations and reporting

- E1 and E2 from v4 apply unchanged to the replacement reader.
- The unanswered rule from v4 applies unchanged.
- The report names all four readers, and lists Ling's partial run with its single unanswered question.
- The run is published whatever the outcome.
