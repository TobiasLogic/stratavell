## Reader v1 and v2 question slots, dry run with the corrected keys

Reader model: dry run, no model calls. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.

| strategy | questions | strict correct | strict accuracy % | value recovered (lenient) | evidence available | overflowed | lenient correct when visible | lenient correct when not visible |
|---|---|---|---|---|---|---|---|---|
| A-uncompressed | 64 | 0 | 0.0 | 0 | 0 | 64 | 0/0 | 0/0 |
| B-conventional | 64 | 0 | 0.0 | 0 | 15 | 23 | 0/15 | 0/26 |
| C-noa | 64 | 0 | 0.0 | 0 | 9 | 48 | 0/9 | 0/7 |
| D-masking | 64 | 0 | 0.0 | 0 | 0 | 64 | 0/0 | 0/0 |
| E-sync-summary | 64 | 0 | 0.0 | 0 | 5 | 0 | 0/5 | 0/59 |
| F-async-progressive | 64 | 0 | 0.0 | 0 | 5 | 0 | 0/5 | 0/59 |
| G-hybrid | 64 | 0 | 0.0 | 0 | 64 | 0 | 0/31 | 0/33 |
