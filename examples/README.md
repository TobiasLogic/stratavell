# Examples

Each example appends a short exchange (a user instruction, a tool call, its result), assembles a request in a provider format, searches the archive and prints the task state.

| example | transport | run |
|---|---|---|
| `go/` | in-process SDK | `go run ./examples/go` |
| `python/` | HTTP over a Unix socket or TCP, standard library only | `stratavell serve --db /tmp/demo.db --socket $XDG_RUNTIME_DIR/stratavell.sock` then `python3 examples/python/example.py $XDG_RUNTIME_DIR/stratavell.sock` |
| `typescript/` | HTTP with `fetch`, Node 22 or newer | `stratavell serve --db /tmp/demo.db` then `cd examples/typescript && node --experimental-strip-types example.ts` |
| `rust/` | HTTP with `ureq` | `stratavell serve --db /tmp/demo.db` then `cd examples/rust && cargo run` |

`examples/python/stratavell_client.py` and `examples/typescript/stratavell.ts` are small clients you can copy into a project. Set `STRATAVELL_TOKEN` when the server was started with a token, and `STRATAVELL_URL` to point the TypeScript and Rust examples at another address.
