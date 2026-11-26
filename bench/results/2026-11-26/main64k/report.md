## Main comparison, 64k window

Window 64000 tokens (effective input capacity 53700), 300 turns per session, 5 seeds, summarizer extractive (deterministic, no model), 6 s simulated per turn.

| strategy | input Mtok | input cost $ | cache hit % | max ctx k | mean ctx k | overflow turns | fg wait s | probes visible % | probes recoverable % | standing rules % | tool Mtok sent | summarizer Mtok | extra primary calls | engine p95 ms | exact recovery % |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| A-uncompressed | 0.12 ± 0.06 | 0.12 ± 0.02 | 57.3 ± 18.0 | 44.2 ± 6.1 | 21.1 ± 5.8 | 294 ± 2 | 0.0 | 0.0 | 0.0 | 1.4 ± 0.4 | 0.12 ± 0.06 | 0.00 | 0 | 0.25 ± 0.04 | 100.0 |
| B-conventional | 8.61 ± 0.37 | 11.42 ± 0.67 | 48.9 ± 1.9 | 53.3 ± 0.4 | 36.0 ± 0.8 | 56 ± 10 | 824.9 ± 32.2 | 26.4 ± 6.8 | 26.4 ± 6.8 | 61.5 ± 2.6 | 7.44 ± 0.32 | 0.64 ± 0.02 | 0 | 0.00 | n/a |
| C-noa | 7.32 ± 1.55 | 5.67 ± 0.75 | 22.1 ± 4.4 | 53.5 ± 0.1 | 35.5 ± 0.8 | 194 ± 21 | 555.5 ± 133.9 | 10.3 ± 5.0 | 10.3 ± 5.0 | 29.0 ± 4.1 | 2.64 ± 0.48 | 0.26 ± 0.06 | 24 ± 5 | 0.00 | n/a |
| D-masking | 0.30 ± 0.16 | 0.56 ± 0.42 | 36.2 ± 30.6 | 48.8 ± 5.4 | 30.3 ± 8.5 | 291 ± 3 | 0.0 | 0.7 ± 1.6 | 0.7 ± 1.6 | 2.4 ± 0.8 | 0.24 ± 0.12 | 0.00 | 0 | 1.44 ± 0.16 | 100.0 |
| E-sync-summary | 8.75 ± 0.07 | 11.41 ± 0.49 | 49.9 ± 2.4 | 45.6 ± 0.1 | 29.5 ± 0.2 | 0 | 322.5 ± 23.5 | 12.1 ± 3.0 | 12.1 ± 3.0 | 21.1 ± 3.5 | 7.34 ± 0.12 | 0.31 ± 0.02 | 0 | 11.97 ± 1.72 | 100.0 |
| F-async-progressive | 9.18 ± 0.05 | 13.04 ± 0.43 | 45.0 ± 1.7 | 45.5 ± 0.1 | 31.0 ± 0.2 | 0 | 0.0 | 12.9 ± 2.8 | 12.9 ± 2.8 | 22.2 ± 3.2 | 7.71 ± 0.13 | 0.31 ± 0.01 | 0 | 7.79 ± 0.74 | 100.0 |
| G-hybrid | 7.45 ± 0.03 | 6.43 ± 0.21 | 68.1 ± 1.2 | 36.1 ± 1.1 | 25.2 ± 0.1 | 0 | 0.0 | 49.5 ± 1.7 | 100.0 | 100.0 | 3.52 ± 0.10 | 0.42 ± 0.02 | 0 | 2.94 ± 0.90 | 100.0 |

Probe recovery by fact kind (recoverable / asked, summed over seeds):

| strategy | constraint | decision | rejected | value | symbol | error |
|---|---|---|---|---|---|---|
| A-uncompressed | 0/140 | 0/45 | 0/45 | 0/90 | 0/45 | 0/31 |
| B-conventional | 82/140 | 0/45 | 1/45 | 4/90 | 3/45 | 15/31 |
| C-noa | 27/140 | 5/45 | 2/45 | 2/90 | 1/45 | 4/31 |
| D-masking | 2/140 | 0/45 | 0/45 | 0/90 | 0/45 | 1/31 |
| E-sync-summary | 24/140 | 0/45 | 1/45 | 2/90 | 6/45 | 15/31 |
| F-async-progressive | 26/140 | 0/45 | 2/45 | 2/90 | 5/45 | 16/31 |
| G-hybrid | 140/140 | 45/45 | 45/45 | 90/90 | 45/45 | 31/31 |

Wall time 248.0 s.
