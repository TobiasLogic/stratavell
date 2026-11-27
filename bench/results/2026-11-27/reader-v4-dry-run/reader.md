## Reader v4 question slots, dry run with the corrected keys

Reader model: dry run, no model calls. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.

| strategy | questions | strict correct | strict accuracy % | value recovered (lenient) | evidence available | overflowed | lenient correct when visible | lenient correct when not visible |
|---|---|---|---|---|---|---|---|---|
| G-hybrid | 33 | 0 | 0.0 | 0 | 33 | 0 | 0/18 | 0/15 |
| F-async-progressive+retrieval | 33 | 0 | 0.0 | 0 | 33 | 0 | 0/5 | 0/28 |
| B-conventional+archive | 33 | 0 | 0.0 | 0 | 33 | 0 | 0/13 | 0/20 |
