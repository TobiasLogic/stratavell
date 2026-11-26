# Releasing

A release is one tag, `vX.Y.Z`. Pushing it starts three workflows:

| workflow | publishes | needs |
|---|---|---|
| `release.yml` | Linux, macOS and Windows archives with `SHA256SUMS`, as a **draft** GitHub release | nothing |
| `publish-image.yml` | `ghcr.io/tobiaslogic/stratavell:X.Y.Z` and `:latest` (linux/amd64, linux/arm64); then the MCP Registry entry | the MCP step runs only when the repository variable `PUBLISH_MCP_REGISTRY` is `true` |
| `publish-packages.yml` | `stratavell` on PyPI, and `stratavell` staged on npm for approval | the PyPI step runs only when `PUBLISH_PYPI` is `true`, and the npm step only when `PUBLISH_NPM` is `true` |

Each workflow can be rerun for an existing tag from the Actions tab (`workflow_dispatch` with the tag).

## Per release

1. Set the same version in `server/server.go`, `clients/python/pyproject.toml`, `clients/python/src/stratavell/__init__.py`, `clients/typescript/package.json` (`npm version X.Y.Z --no-git-tag-version`) and `server.json` (`version` and the image tag). `./scripts/check-versions.sh` checks them, and CI runs it.
2. Add the `CHANGELOG.md` entry and `docs/releases/vX.Y.Z.md`. The first line of the notes becomes the release title.
3. Push `main` and wait for CI to pass.
4. `git tag -a vX.Y.Z -m "Stratavell vX.Y.Z"` and `git push origin vX.Y.Z`.
5. Check the draft release, verify the archives against `SHA256SUMS`, and publish it.

## One-time setup

Container image:
- The image carries an `org.opencontainers.image.source` label that links the package to this public repository. For v0.1.2 the package was public right after the first push.
- If it is ever private, open the `stratavell` package under the GitHub profile's Packages tab and set Package settings, visibility to **public**. The MCP Registry only lists publicly pullable servers.

MCP Registry:
- Ownership is proven by the image label `io.modelcontextprotocol.server.name`, which must equal `name` in `server.json`.
- The workflow signs in with GitHub OIDC, so no secret is needed.
- Once the image is public, set the repository variable `PUBLISH_MCP_REGISTRY` to `true` (Settings, Secrets and variables, Actions, Variables).

PyPI, with trusted publishing and no API token:
1. On pypi.org, add a pending trusted publisher with:
   - project `stratavell`
   - owner `TobiasLogic`
   - repository `stratavell`
   - workflow `publish-packages.yml`
   - environment `pypi`
2. In the GitHub repository, create an environment named `pypi`.
3. Set `PUBLISH_PYPI` to `true`.

npm:
1. npm trusted publishing is configured on an existing package, so the first version is published once by hand. In `clients/typescript`, run `npm login`, then `npm publish --access public`.
2. In the package settings on npmjs.com, add a trusted publisher with:
   - repository `TobiasLogic/stratavell`
   - workflow `publish-packages.yml`
   - environment `npm`
   - leave "Allow npm publish" unchecked, so CI can only stage a release
3. Create a GitHub environment named `npm`, and set `PUBLISH_NPM` to `true`.
   npm marks a new trusted publisher "pending validation" until CI publishes through it once, and gives about two days to do so. If that window passes, delete the connection and add it again before the next release.
4. Each release then appears under Staged Packages in the npm account settings. A maintainer approves it there with two-factor authentication before it goes live, so a compromised CI run cannot publish on its own.

Until these variables are set, the publishing jobs are skipped instead of failing.
