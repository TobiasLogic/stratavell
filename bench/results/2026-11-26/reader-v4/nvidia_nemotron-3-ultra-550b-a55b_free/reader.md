## 

Reader model: nvidia/nemotron-3-ultra-550b-a55b:free via OpenRouter, temperature 0, max 1500 output tokens. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.

| strategy | questions | strict correct | strict accuracy % | value recovered (lenient) | evidence available | overflowed | lenient correct when visible | lenient correct when not visible |
|---|---|---|---|---|---|---|---|---|
| G-hybrid | 33 | 33 | 100.0 | 33 | 33 | 0 | 18/18 | 15/15 |
| F-async-progressive+retrieval | 33 | 32 | 97.0 | 32 | 33 | 0 | 5/6 | 27/27 |
| B-conventional+archive | 33 | 33 | 100.0 | 33 | 33 | 0 | 13/13 | 20/20 |
