## Reader test: extractive compaction, 64k window, seeds 1 and 2

Reader model: anthropic/claude-haiku-5.5 via OpenRouter, temperature 0, 600-token cap. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.

| strategy | questions | correct | accuracy % | overflowed | correct when fact visible | correct when not visible |
|---|---|---|---|---|---|---|
| A-uncompressed | 64 | 0 | 0.0 | 64 | 0/0 | 0/0 |
| B-conventional | 64 | 15 | 23.4 | 23 | 15/15 | 0/26 |
| C-noa | 64 | 9 | 14.1 | 48 | 9/9 | 0/7 |
| D-masking | 64 | 0 | 0.0 | 64 | 0/0 | 0/0 |
| E-sync-summary | 64 | 5 | 7.8 | 0 | 5/6 | 0/58 |
| F-async-progressive | 64 | 5 | 7.8 | 0 | 5/7 | 0/57 |
| G-hybrid | 64 | 64 | 100.0 | 0 | 31/31 | 33/33 |
