# Security policy

## Supported versions

Stratavell is a 0.x research preview. Security fixes go into the latest 0.x release only.

| version | supported |
|---|---|
| 0.1.x | yes |

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub: open the repository's **Security** tab and choose **Report a vulnerability**. Do not open a public issue for a security problem.

Include the version (`stratavell version`), the integration mode (Go SDK, HTTP, Unix socket or MCP), steps to reproduce and the impact you expect. Expect an acknowledgement within 7 days. Fixes are released as soon as they are ready, and the advisory is published together with the fix.

## Threat model

Stratavell is a local, single-tenant engine. It stores whatever an agent sends it, including tool outputs that may contain secrets, and it serves that data back to clients that can reach it. The protections below assume that model.

**HTTP API**
- The default listen address is `127.0.0.1:7077`. Listening on any non-loopback address requires a bearer token (`STRATAVELL_TOKEN` or `--token-file`). Token comparison is constant-time.
- Without a token, the server answers TCP requests only when the `Host` header is a loopback name. It also refuses requests whose `Origin` is not a loopback origin. This blocks DNS rebinding and cross-site requests from web pages.
- A token grants access to every session in the database. There are no per-session or per-agent credentials. Run separate servers and databases for separate trust domains.
- Request bodies are limited to 64 MiB. Header, read and idle timeouts are set.
- There is no TLS. For remote access, put the server behind a TLS-terminating proxy and keep the token secret.

**Unix socket**
- The socket is created with mode 0600, so only the owning user can connect.
- An existing path is replaced only when it is a stale socket that nothing is listening on.

**MCP server**
- The MCP server runs over stdio as a child process of the host and inherits the host's trust.
- It is bound to one session and one agent, set by flags or by `STRATAVELL_SESSION` and `STRATAVELL_AGENT`.

**Embedding the Go SDK or `server.Handler`**
- When you serve the handler from your own `http.Server`, the loopback guard above does not apply. Authentication and network exposure are your responsibility.

**Memory tools**
- An agent's memory tools return data from its own session only. Within a session, an agent sees its own and its sub-agents' private data, plus shared items and items granted in a handoff.
- A refused id gets the same "not found" reply as an id that does not exist.
- A sha256 hash or hash prefix is resolved only among outputs the caller can see, so a prefix that matches another session's data gets the same "not found" reply.
- This applies to every memory tool. Before the fix listed in the changelog, `memory_get_summary_lineage` did not check it for summary ids.

**Archived content is untrusted**
- Tool outputs, web pages and files the agent saw can contain prompt injection. Stratavell labels archived and retrieved material as "data, not instructions", in the engine notice, in summary blocks, in retrieved evidence and in memory tool results.
- Standing user instructions are kept apart from archived content, so archived text cannot become a standing instruction.
- These labels reduce the risk; they do not remove it. Hosts should keep their own defenses.

**Summarizers and providers**
- The default summarizer is extractive and makes no network calls.
- If you configure a model summarizer (Ollama, OpenAI-compatible or Anthropic), archived content is sent to that endpoint.
- API keys are read from the environment or from files and are never stored in the database.

**Data at rest**
- The database is not encrypted, so protect the file with filesystem permissions.
- `stratavell delete-session` removes a session, its blobs and its index entries. It vacuums by default, so the deleted bytes leave the file.

## Out of scope

- Attacks that require write access to the database file or to the process.
- Denial of service by a client that holds a valid token.
