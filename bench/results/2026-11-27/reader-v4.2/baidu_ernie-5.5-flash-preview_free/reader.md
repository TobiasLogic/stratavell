## Reader v4.2, baidu/ernie-5.5-flash-preview:free

Reader model: baidu/ernie-5.5-flash-preview:free via OpenRouter, temperature 0, max 1500 output tokens. Each row is one question asked at a probe point with that strategy's assembled context; overflowed requests count as wrong.

| strategy | questions | strict correct | strict accuracy % | value recovered (lenient) | evidence available | overflowed | lenient correct when visible | lenient correct when not visible |
|---|---|---|---|---|---|---|---|---|
| F-async-progressive+retrieval | 1 | 1 | 100.0 | 1 | 1 | 0 | 0/0 | 1/1 |
