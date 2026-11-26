# Contributing to Stratavell

Thanks for your interest. Stratavell is a research preview: bug reports, reproductions, benchmark critiques and small focused pull requests are the most useful contributions right now.

## Before you start

- For anything larger than a bug fix, open an issue first and describe the problem and the approach. The engine has invariants that are easy to break from the outside: exact recovery of every committed event, tool-call pairing, and generation compare-and-swap publishing.
- Security problems go through private reporting, not public issues. See [SECURITY.md](SECURITY.md).

## Development setup

You need Go at the version in `go.mod` or newer. No C toolchain, external database or API key is required: SQLite is the pure-Go `modernc.org/sqlite`.

```bash
git clone https://github.com/TobiasLogic/stratavell
cd stratavell
go build ./...
go vet ./...
go test ./...
go test -race -short ./...
```

`go test ./...` includes the crash-recovery test, a SIGKILL during writes, and `TestDeterministicSmoke`, a 12-second deterministic benchmark run compared field by field with `bench/testdata/smoke-golden.json`. `-short` skips the smoke run, which is slow under the race detector.

Fuzz targets:

```bash
go test ./engine -run '^$' -fuzz FuzzAppendAssemble -fuzztime 60s
go test ./engine -run '^$' -fuzz FuzzSearchQuery -fuzztime 60s
go test ./msg -run '^$' -fuzz FuzzMessage -fuzztime 60s
go test ./internal/textproc -run '^$' -fuzz FuzzDigest -fuzztime 60s
```

The cross-language examples run against a local server; `scripts/examples-smoke.sh` starts one and runs all four.

## Code style

- `gofmt` everything. CI rejects unformatted files.
- Match the surrounding code: small functions and descriptive names. The codebase uses no explanatory comments, so keep it that way and put the reasoning in the pull request or in `docs/`.
- Keep the engine provider-neutral. Provider-specific behavior belongs in `adapters/`.
- New behavior needs a test. Concurrency changes need a test that runs cleanly under `-race`.

## Benchmarks and results

- If your change alters deterministic benchmark output, `TestDeterministicSmoke` fails and prints the difference. When the change is intended, regenerate the golden file with `go test ./bench -run TestDeterministicSmoke -update-golden` and explain the change in the pull request and in `CHANGELOG.md`.
- Never edit or delete files under `bench/results/`. New results go in a new dated directory, together with the commit they came from, and get logged in `docs/experiments.md`. That includes unfavorable, failed and superseded runs.
- Real-model benchmarks cost money and are never required for a pull request.
- Claims in documentation must name their conditions: workload, window, seeds, and whether costs are simulated or billed.

## Pull requests

- One topic per pull request, with a description of what changed and how you tested it.
- Note any change to the HTTP API, MCP tools, policy format or database schema. Before 1.0 such changes are allowed but must be listed in `CHANGELOG.md` with migration steps.

## License

By contributing, you agree that your contributions are licensed under the Apache License 2.0, the same license as the project (see [LICENSE](LICENSE), section 5).
