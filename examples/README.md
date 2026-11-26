# Examples

Each example appends a short exchange (a user instruction, a tool call, its result), assembles a request in a provider format, searches the archive and prints the task state.

| example | transport | run |
|---|---|---|
| `go/` | in-process SDK | `go run ./examples/go` |
| `python/` | HTTP over a Unix socket or TCP, standard library only | `palimpsest serve --db /tmp/demo.db --socket $XDG_RUNTIME_DIR/palimpsest.sock` then `python3 examples/python/example.py $XDG_RUNTIME_DIR/palimpsest.sock` |
| `typescript/` | HTTP with `fetch`, Node 22 or newer | `palimpsest serve --db /tmp/demo.db` then `cd examples/typescript && node --experimental-strip-types example.ts` |
| `rust/` | HTTP with `ureq` | `palimpsest serve --db /tmp/demo.db` then `cd examples/rust && cargo run` |

`examples/python/palimpsest_client.py` and `examples/typescript/palimpsest.ts` are small clients you can copy into a project. Set `PALIMPSEST_TOKEN` when the server was started with a token, and `PALIMPSEST_URL` to point the TypeScript and Rust examples at another address.
