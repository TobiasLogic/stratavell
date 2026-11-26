## What changed

## Why

## How it was tested

- [ ] `go vet ./...` and `go test ./...` pass
- [ ] `go test -race -short ./...` passes (for concurrency changes)
- [ ] `TestDeterministicSmoke` is unchanged, or the golden file was regenerated and the change is explained below and in `CHANGELOG.md`

## Compatibility

- [ ] No change to the HTTP API, MCP tools, policy format or database schema
- [ ] Or: the change is listed in `CHANGELOG.md` with migration steps
