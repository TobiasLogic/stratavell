## Superseded reader run v1, extractive compaction (graded with the final keys)

Reader model: superseded v1: 120-token cap and short-answer prompt. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.

| strategy | questions | correct | accuracy % | overflowed | correct when fact visible | correct when not visible |
|---|---|---|---|---|---|---|
| A-uncompressed | 64 | 0 | 0.0 | 64 | 0/0 | 0/0 |
| B-conventional | 64 | 14 | 21.9 | 23 | 14/15 | 0/26 |
| C-noa | 64 | 8 | 12.5 | 48 | 8/9 | 0/7 |
| D-masking | 64 | 0 | 0.0 | 64 | 0/0 | 0/0 |
| E-sync-summary | 64 | 5 | 7.8 | 0 | 5/6 | 0/58 |
| F-async-progressive | 64 | 5 | 7.8 | 0 | 5/6 | 0/58 |
| G-hybrid | 64 | 48 | 75.0 | 0 | 28/30 | 20/34 |
