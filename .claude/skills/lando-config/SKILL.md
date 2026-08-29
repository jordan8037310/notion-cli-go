---
name: lando-config
description: Use when working with this repo's Go toolchain — building, testing, linting, or running notioncli. Lando provides the Go 1.21 runtime inside Docker so contributors do not need Go installed on the host. Invoke whenever a task would otherwise need `go`, `gofmt`, `gopls`, or `golangci-lint` locally.
license: MIT
metadata:
  version: "1.0.0"
  domain: tooling
  scope: environment
  triggers: lando, .lando.yml, go runtime, go 1.21, docker runtime, go test, go build, gofmt, golangci-lint, reproducible env, appserver
---

# Lando Config (notion-cli-go)

Project-scoped skill. Encodes how this repo uses Lando as its primary Go toolchain.

## Role

You are a reproducible-environments specialist for the `notion-cli-go` project. Your job is to keep dev, test, and CI behavior aligned using Lando v3's Go service, so contributors can work on the CLI without installing Go on the host.

## When to use

- Any task that would otherwise require `go`, `gofmt`, `gopls`, or `golangci-lint` installed on the host.
- Setting up a fresh clone for a contributor.
- Debugging test failures that pass locally but fail in CI (or vice versa).
- Modifying `.lando.yml` — scope, service type, tooling, env passthrough, caches.
- Onboarding: explaining the runtime model to a human or agent.

## Two runtimes, one source of truth

This repo supports **both** a local Go toolchain and the Lando-hosted one. CI is the tiebreaker.

| Mode | Invocation | Use when |
|---|---|---|
| Host Go (if installed) | `make test`, `make ci` | You have `go 1.21` on the host and want the fastest LSP loop. |
| Lando (authoritative for env parity) | `lando test`, `lando ci` | You don't have Go on the host, or you're debugging a CI-only failure. |
| Makefile hybrid | `make test GO="lando go"` | You want Make's ergonomics but containerized execution. |

CI (`.github/workflows/test.yml`) uses host Go 1.21 on `ubuntu-latest`. The Lando image pins `go:1.21` — keep these in lockstep.

## Canonical commands

Run from the repo root.

```sh
# First-time setup
lando start                    # Pulls the go:1.21 image, boots appserver

# Core workflow
lando test                     # go test ./...
lando test-race                # go test -race ./...
lando build                    # go build -v ./...
lando vet                      # go vet ./...
lando fmt-check                # fails if any file is not gofmt-clean
lando cover                    # coverage profile + per-func summary
lando check-test-gaps          # flags exported functions without a matching Test*
lando ci                       # fmt-check + vet + race tests + gap gate (matches CI)

# Linting (installs golangci-lint into GOPATH on first use)
lando lint

# Raw tooling
lando go <any-go-cmd>          # e.g. lando go mod tidy, lando go doc fmt.Errorf
lando gofmt -l .
lando gopls ...

# Run the CLI itself inside the container
lando cli list                 # → go run . list
lando cli add "buy milk"       # → go run . add "buy milk"
lando shell                    # bash shell in appserver
```

## Environment variables

Lando auto-loads `.env` at the project root and passes it to the appserver. The container expects:

| Var | Purpose | Source |
|---|---|---|
| `NOTION_API_KEY` | Notion integration token | Notion → My integrations |
| `NOTION_PAGE_ID` | Default page target | Page URL suffix |
| `LOCAL_TIMEZONE` | Formatting of last-edited timestamps | e.g. `America/New_York` |

`.env.example` in the repo root has the template. Copy to `.env` and fill in — `.env` is gitignored.

## Architecture (what's in `.lando.yml`)

- `services.appserver`: `type: go:1.21` — Lando's official Go service. Mounts the repo at `/app`.
- `build_as_root`: installs `bash`, `git`, `make`, and CA certs so the scripts in `scripts/` run.
- `run`: primes `go mod download` on container build — first test run is then near-instant.
- Module/build caches are pinned to `/app/.lando/go/*` inside the container, so they survive `lando rebuild` but don't pollute the host.
- `tooling.*`: one block per shortcut. Invoke with `lando <tooling-name>`.

## Adding a new tooling shortcut

When the Makefile grows a new target that developers will run frequently, mirror it in `.lando.yml` under `tooling:`. Minimum shape:

```yaml
tooling:
  my-task:
    service: appserver
    cmd: <the command, using sh -c '...' if you need shell features>
    description: <short human-readable blurb — shown in `lando` help>
```

Rules:
- Use `sh -c '...'` (not bash) unless you specifically need bashisms. Keeps the command portable inside the service image.
- Do NOT hardcode paths like `/app` — `cmd` runs from the appserver's working dir, which is already the project root.
- If the shortcut runs scripts from `scripts/`, keep the `bash scripts/<name>.sh` invocation; the container has bash.

## Troubleshooting

**`lando start` hangs or fails to pull image**
- Check Docker Desktop is running: `docker version`.
- Nuke and retry: `lando destroy -y && lando rebuild -y`.

**Host CI works, Lando CI fails (or vice versa)**
- Check Go version parity: `lando go version` must match `.github/workflows/test.yml` `go-version`.
- Suspect module cache corruption: `lando ssh -c "go clean -modcache"` then `lando test`.

**Tests can't reach the Notion API**
- Only an issue for integration tests (this repo doesn't have any — all tests use `httptest`). If you add real-network tests, the appserver has outbound network by default; nothing special needed.

**`lando lint` installs golangci-lint every time**
- It shouldn't — the tooling command checks `command -v golangci-lint` first and only installs if missing. If it keeps reinstalling, `GOPATH` inside the container is being wiped. Check the `environment.GOPATH` block in `.lando.yml` still points at `/app/.lando/go`.

**Permission errors on coverage.out or notioncli binary after running from both host + Lando**
- The container may write files owned by root. Fix: `lando ssh -c "chown -R $(id -u):$(id -g) ."` or delete the offending file.

## Do not

- **Do not commit `.env`.** It's in `.gitignore`; keep it that way.
- **Do not add a database, cache, or second service** unless we actually need one. This repo is single-service by design.
- **Do not pin the Go version outside `.lando.yml` and `.github/workflows/test.yml`.** Two places must agree; anywhere else is lying.
- **Do not replace the Makefile with Lando-only targets.** The Makefile is the portable entry point; Lando is the containerized runner. Both stay.
- **Do not install Go inside `build_as_root`.** The `go:1.21` service type already includes it. Installing a second copy causes PATH ambiguity.

## Related skills

- `golang-pro` — idiomatic Go dev (loaded by the `go-developer` agent)
- `test-master` — testing strategy (loaded by the `go-unit-tester` agent)
