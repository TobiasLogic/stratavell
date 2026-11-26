## Component-matched comparison, 64k window

Window 64000 tokens (effective input capacity 53700), 300 turns per session, 5 seeds, summarizer extractive (deterministic, no model), 6 s simulated per turn.

| strategy | completion % | input Mtok | input cost $ | total cost $ | cache hit % | max ctx k | mean ctx k | overflow turns | fg wait s | probes visible % | probes grounded % | probes recoverable % | standing rules % | tool Mtok sent | summarizer Mtok | extra primary calls | engine p95 ms | exact recovery % |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| B-conventional | 81.4 ± 3.4 | 8.61 ± 0.37 | 11.42 ± 0.67 | 11.50 ± 0.68 | 48.9 ± 1.9 | 53.3 ± 0.4 | 36.0 ± 0.8 | 56 ± 10 | 824.9 ± 32.2 | 26.4 ± 6.8 | 26.4 ± 6.8 | 26.4 ± 6.8 | 61.5 ± 2.6 | 7.44 ± 0.32 | 0.64 ± 0.02 | 0 | 0.00 | n/a |
| E-sync-summary | 100.0 | 8.75 ± 0.07 | 11.41 ± 0.49 | 11.44 ± 0.49 | 49.9 ± 2.4 | 45.6 ± 0.1 | 29.5 ± 0.2 | 0 | 322.5 ± 23.5 | 12.1 ± 3.0 | 11.6 ± 3.4 | 11.6 ± 3.4 | 21.1 ± 3.5 | 7.34 ± 0.12 | 0.31 ± 0.02 | 0 | 11.79 ± 0.40 | 100.0 |
| F-async-progressive | 100.0 | 9.18 ± 0.05 | 13.04 ± 0.43 | 13.07 ± 0.43 | 45.0 ± 1.7 | 45.5 ± 0.1 | 31.0 ± 0.2 | 0 | 0.0 | 12.9 ± 2.8 | 12.6 ± 3.1 | 12.6 ± 3.1 | 22.2 ± 3.2 | 7.71 ± 0.13 | 0.31 ± 0.01 | 0 | 8.47 ± 1.21 | 100.0 |
| B-conventional+pins | 81.0 ± 3.7 | 8.58 ± 0.44 | 11.38 ± 0.83 | 11.46 ± 0.83 | 48.9 ± 2.2 | 53.4 ± 0.4 | 36.0 ± 0.9 | 57 ± 11 | 830.1 ± 31.9 | 33.7 ± 8.5 | 33.7 ± 8.5 | 33.7 ± 8.5 | 81.0 ± 3.9 | 7.37 ± 0.41 | 0.64 ± 0.02 | 0 | 0.00 | n/a |
| B-conventional+archive | 81.4 ± 3.4 | 8.92 ± 0.38 | 11.38 ± 0.67 | 11.46 ± 0.67 | 51.0 ± 1.9 | 53.3 ± 0.4 | 36.0 ± 0.8 | 56 ± 10 | 824.9 ± 32.2 | 26.4 ± 6.8 | 26.4 ± 6.8 | 80.0 ± 20.9 | 61.5 ± 2.6 | 7.44 ± 0.32 | 0.64 ± 0.02 | 0 | 0.00 | n/a |
| E-sync-summary+retrieval | 100.0 | 8.92 ± 0.12 | 11.36 ± 0.55 | 11.40 ± 0.55 | 51.1 ± 2.5 | 45.6 ± 0.1 | 30.0 ± 0.4 | 0 | 320.1 ± 27.2 | 12.6 ± 3.5 | 12.1 ± 3.7 | 97.2 ± 1.3 | 22.0 ± 3.6 | 7.14 ± 0.13 | 0.31 ± 0.02 | 0 | 11.77 ± 0.74 | 100.0 |
| F-async-progressive+retrieval | 100.0 | 9.36 ± 0.10 | 12.88 ± 0.44 | 12.91 ± 0.44 | 46.9 ± 1.6 | 45.6 ± 0.0 | 31.5 ± 0.3 | 0 | 0.0 | 12.8 ± 3.1 | 12.6 ± 3.4 | 97.2 ± 1.3 | 21.9 ± 2.8 | 7.53 ± 0.16 | 0.30 ± 0.01 | 0 | 8.88 ± 0.82 | 100.0 |
| G-hybrid | 100.0 | 7.45 ± 0.03 | 6.43 ± 0.20 | 6.48 ± 0.21 | 68.1 ± 1.2 | 36.1 ± 1.1 | 25.2 ± 0.1 | 0 | 0.0 | 49.5 ± 1.7 | 47.5 ± 1.5 | 100.0 | 100.0 | 3.52 ± 0.10 | 0.42 ± 0.02 | 0 | 2.91 ± 0.50 | 100.0 |

Probe recovery by fact kind (recoverable / asked, summed over seeds):

| strategy | constraint | decision | rejected | value | symbol | error |
|---|---|---|---|---|---|---|
| B-conventional | 82/140 | 0/45 | 1/45 | 4/90 | 3/45 | 15/31 |
| E-sync-summary | 24/140 | 0/45 | 1/45 | 2/90 | 4/45 | 15/31 |
| F-async-progressive | 26/140 | 0/45 | 2/45 | 2/90 | 4/45 | 16/31 |
| B-conventional+pins | 112/140 | 0/45 | 1/45 | 4/90 | 3/45 | 14/31 |
| B-conventional+archive | 115/140 | 37/45 | 37/45 | 74/90 | 37/45 | 18/31 |
| E-sync-summary+retrieval | 140/140 | 45/45 | 45/45 | 90/90 | 45/45 | 20/31 |
| F-async-progressive+retrieval | 140/140 | 45/45 | 45/45 | 90/90 | 45/45 | 20/31 |
| G-hybrid | 140/140 | 45/45 | 45/45 | 90/90 | 45/45 | 31/31 |

Wall time 365.1 s.
