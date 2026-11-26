## Main comparison, 200k window

Window 200000 tokens (effective input capacity 179700), 400 turns per session, 3 seeds, summarizer extractive (deterministic, no model), 6 s simulated per turn.

| strategy | input Mtok | input cost $ | cache hit % | max ctx k | mean ctx k | overflow turns | fg wait s | probes visible % | probes recoverable % | standing rules % | tool Mtok sent | summarizer Mtok | extra primary calls | engine p95 ms | exact recovery % |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| A-uncompressed | 1.91 ± 0.62 | 0.60 ± 0.09 | 90.3 ± 3.1 | 173.5 ± 10.6 | 91.0 ± 13.3 | 379 ± 4 | 0.0 | 0.0 | 0.0 | 3.9 ± 0.8 | 1.76 ± 0.57 | 0.00 | 0 | 0.28 ± 0.03 | 100.0 |
| B-conventional | 38.73 ± 1.19 | 16.46 ± 1.10 | 86.5 ± 0.7 | 161.4 ± 0.3 | 97.7 ± 3.0 | 0 | 298.3 ± 34.7 | 36.0 ± 0.2 | 36.0 ± 0.2 | 75.9 ± 0.4 | 35.97 ± 1.18 | 0.45 ± 0.01 | 0 | 0.00 | n/a |
| C-noa | 43.98 ± 2.62 | 30.90 ± 2.79 | 60.2 ± 3.7 | 176.1 ± 3.0 | 96.5 ± 4.7 | 6 ± 2 | 726.3 ± 120.5 | 36.3 ± 0.4 | 36.3 ± 0.4 | 79.8 ± 4.5 | 30.91 ± 1.75 | 0.39 ± 0.05 | 26 ± 5 | 0.00 | n/a |
| D-masking | 17.82 ± 2.42 | 44.02 ± 6.29 | 1.3 ± 0.8 | 179.1 ± 0.4 | 123.7 ± 2.2 | 253 ± 23 | 0.0 | 11.0 ± 3.2 | 11.0 ± 3.2 | 30.5 ± 5.4 | 11.39 ± 1.36 | 0.00 | 0 | 1.75 ± 0.18 | 100.0 |
| E-sync-summary | 32.77 ± 0.61 | 17.82 ± 0.35 | 81.5 ± 0.5 | 150.9 ± 2.5 | 82.4 ± 1.5 | 0 | 382.9 ± 23.0 | 41.9 ± 7.5 | 41.9 ± 7.5 | 79.5 ± 0.7 | 25.84 ± 0.82 | 0.52 ± 0.03 | 0 | 16.45 ± 0.93 | 100.0 |
| F-async-progressive | 39.54 ± 0.55 | 27.62 ± 0.80 | 75.0 ± 1.0 | 152.5 ± 0.2 | 99.4 ± 1.4 | 0 | 0.0 | 47.4 ± 6.3 | 47.4 ± 6.3 | 78.1 ± 0.3 | 32.43 ± 0.46 | 0.52 ± 0.02 | 0 | 4.47 ± 1.45 | 100.0 |
| G-hybrid | 30.59 ± 0.80 | 10.44 ± 0.32 | 89.9 ± 0.2 | 124.2 ± 1.7 | 77.3 ± 2.0 | 0 | 0.0 | 56.8 ± 2.2 | 100.0 | 100.0 | 18.84 ± 0.67 | 0.49 ± 0.02 | 0 | 3.04 ± 0.51 | 100.0 |

Probe recovery by fact kind (recoverable / asked, summed over seeds):

| strategy | constraint | decision | rejected | value | symbol | error |
|---|---|---|---|---|---|---|
| A-uncompressed | 0/99 | 0/33 | 0/33 | 0/66 | 0/33 | 0/25 |
| B-conventional | 72/99 | 0/33 | 0/33 | 4/66 | 3/33 | 25/25 |
| C-noa | 74/99 | 3/33 | 3/33 | 0/66 | 1/33 | 24/25 |
| D-masking | 16/99 | 5/33 | 5/33 | 0/66 | 0/33 | 6/25 |
| E-sync-summary | 75/99 | 9/33 | 3/33 | 0/66 | 11/33 | 23/25 |
| F-async-progressive | 73/99 | 9/33 | 16/33 | 2/66 | 13/33 | 24/25 |
| G-hybrid | 99/99 | 33/33 | 33/33 | 66/66 | 33/33 | 25/25 |

Wall time 282.5 s.
