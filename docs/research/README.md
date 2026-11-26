# Research references

Four notes record what was read, what was verified and what was used:

- `01-apec-masking-benchmarks.md`: CompactionBench and APEC, The Complexity Trap, Benchmarks Are Surprisingly Compaction-Friendly, and a related-work pass over 28 items
- `02-lcm-volt.md`: the LCM paper and the Volt source code
- `03-context-mode-cmv-provider-caching.md`: Context Mode source, Contextual Memory Virtualisation, and the Anthropic, OpenAI and Gemini caching documentation
- `04-norma-noa.md`: Norma's source, its conventional compaction and its noa engine, with reimplementation specs used for baseline C

Numbers in the notes are labeled measured (by the cited authors), claimed, or documented. Code was read, never built or run.

## Primary sources

| source | status | what we took | what we did not take |
|---|---|---|---|
| Liu et al., *Benchmarking Long-Horizon Agent Context Compaction with CompactionBench* (APEC), Microsoft, preprints.org 202611.0780 (read via the Microsoft Research PDF; preprints.org refused automated access) | preprint; benchmark not released | progressive, append-only summaries; publish only at request boundaries; snapshot by sequence; one background job per range; the finding that progressive matters more than asynchronous | the synchronous full-history emergency compaction (replaced by a deterministic ladder); the lossy drop of summarized segments |
| Lindenbauer et al., *The Complexity Trap: Simple Observation Masking Is as Efficient as LLM Summarization for Agent Context Management*, arXiv 2508.21433; code github.com/JetBrains-Research/the-complexity-trap | workshop paper (DL4C at NeurIPS 2025); single run per configuration | masking as the first tier; keeping actions and reasoning, masking observations; placeholders with retrieval handles | the per-step rolling window, which breaks prompt caching; replaced by epoch masking (the `G-rolling-mask` ablation measures the difference) |
| Salle, Bhatnagar et al., *Benchmarks Are Surprisingly Compaction-Friendly*, arXiv 2610.31778; code github.com/aisa-group/compaction | preprint; two runs per configuration | constraint erosion across repeated compactions; evaluate with constraints that exist only in context; rule-aware scoring | none of its strategies directly |
| Ehrlich and Blackman, *LCM: Lossless Context Management*, arXiv 2605.04050 | preprint, no sample sizes | immutable store; summary DAG with ordered edges; exact expansion; engine-injected ids in context; deterministic fallback | its unlocked context rewrite (race), its truncation of large outputs before storage, file storage by path only |
| Volt, github.com/Martian-Engineering/volt (MIT) | read at HEAD 4f227134 and paper-era 944c7943 | schema ideas (ordered summary-source edges, condensation order), tool shapes for grep, describe and expand | code (none copied), prompts (none copied) |
| Context Mode, github.com/mksglu/context-mode (Elastic License 2.0, source-available) | read | content-type-aware handling of large outputs, FTS5 indexing of tool output, prompt-cache-static tool definitions | code and prompts (license forbids hosted use; nothing copied) |
| Santoni, *Contextual Memory Virtualisation*, arXiv 2602.22402, code Apache-2.0 | preprint; one user's 76 sessions | structurally lossless trimming of tool output with the original kept; an explicit cache-penalty versus savings break-even | its manual branch and snapshot workflow |
| Norma, github.com/Autumn-27/norma (Apache-2.0), noa ported from acp-kernel (MIT plus attribution term) | read at d79faab | reimplementation of both compaction algorithms as baselines B and C; design credit for noa's tiered blocks | prompts (noa's are from acp-kernel; Norma's conventional prompt is verbatim proprietary text); none copied |
| Anthropic prompt caching, context editing, token counting, extended and preserved thinking documentation | documented | breakpoint rules, lookback of 20 positions, minimum cacheable lengths, TTL and price multipliers, thinking block preservation and prefix fingerprinting, usage fields | server-side context editing (not implemented) |

## Related work consulted

The pass in `01-apec-masking-benchmarks.md`, section 6, covers 28 items. Most were read at abstract level. The ones that influenced this design:

- ACON
- OpenHands condensers
- SWE-agent history processors
- Anthropic's context engineering articles
- GitHub Copilot's documented background compaction (start at about 80%, block at about 95%)
- Chroma's Context Rot
- OOLONG
- Recursive Language Models
- *Lost in Context Compaction* (CONPACT)
- Slipstream
- TokenPilot (cache-aware batch eviction)
- AgentDiet (removing expired content)
- CliffCompaction (drop, never rephrase, on the hard path)
- MemGPT and Letta

## License position

No third-party code or prompt text was copied. Algorithms were reimplemented from descriptions and from reading code, which the licenses above permit. Norma's noa design is credited here and in `docs/benchmarks.md`.
