## Reader test: Haiku 5.5 summaries, 64k window, seed 1

Reader model: anthropic/claude-haiku-5.5 via OpenRouter, temperature 0, 600-token cap. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.

| strategy | questions | correct | accuracy % | overflowed | correct when fact visible | correct when not visible |
|---|---|---|---|---|---|---|
| E-sync-summary | 33 | 6 | 18.2 | 0 | 6/8 | 0/25 |
| F-async-progressive | 33 | 6 | 18.2 | 0 | 6/8 | 0/25 |
| G-hybrid | 33 | 33 | 100.0 | 0 | 21/21 | 12/12 |
