# Claude Code project config

This directory holds project-scoped Claude Code configuration that applies to
any agent or session working inside `notion-cli-go`. User-level settings
(`~/.claude/settings.json`) still apply; these are additive restrictions.

## Files

- **`settings.json`** — enables the `fullstack-dev-skills` plugin and adds a
  project-scoped `permissions.deny` list. See "What's denied" below.
- **`agents/go-developer.md`** — project-scoped Go implementation agent.
- **`agents/go-unit-tester.md`** — project-scoped Go test agent.
- **`skills/lando-config/SKILL.md`** — project-scoped skill teaching the
  host-Go ↔ Lando runtime model.

## What's denied (and why)

The deny list in `settings.json` is a belt-and-suspenders layer on top of
Jordan's global `~/.claude/settings.json`. It's tuned for this project's
specific risks (a fork with an `upstream` remote pointing at the canonical
repo, env-var secrets for the Notion API, multiple concurrent agents in
worktrees).

### Host-machine protection
- `sudo` anything.
- `rm -rf` against `/`, `~`, `$HOME`, `/Users/*`.
- `chmod -R 777` anywhere dangerous.
- `chown -R`, `mkfs`, raw `dd` to devices.

### Secret protection
- Reads of `.env`, `.env.*`, `.ssh/**`, `.aws/credentials`, `.gcloud/**`,
  `.netrc`, `.npmrc`, `.yarnrc`, `.pypirc`, `.docker/config.json`,
  `id_rsa`, `id_ed25519`, anything with `private*key` or `*secret*`.
- `cat .env*`, `head .env*`, `tail .env*`, `grep =* .env`, etc.
- `echo $*_TOKEN*`, `echo $*_SECRET*`, `echo $NOTION_API_KEY*`,
  `printenv *TOKEN*`, `printenv NOTION_API_KEY*`, `env | grep ...`.
  (If you need to see an env var, do it outside Claude.)

### Git / GitHub safety
- **No pushes to `upstream`** (`kris-hansen/notion-cli-go`). Agents push
  to the `origin` fork only.
- **No force-push to fork's `main`.** Feature branches can rebase freely;
  `main` is supposed to mirror upstream and is protected against
  rewriting its history. This is enforced at the command-pattern level
  for both `--force` and `--force-with-lease`.
- **No `gh pr merge --base main`** directly. PRs to `main` are the
  integration cut; promote `feature/foundation` → `main` via the
  tracking PR (#15) only when the roadmap stabilizes.
- No `gh repo delete`, `gh repo transfer`, `gh auth logout`.
- No `gh secret set/delete`, `gh ssh-key delete`, `gh gpg-key delete`.
- No `git clean -fdx`, `git reset --hard` to upstream/origin main,
  `git branch -D main`.

### System / tooling
- No `brew uninstall`, `brew services stop`.
- No `npm publish/unpublish`.
- No `go clean -modcache`.
- No `docker system prune`, `docker volume rm`.
- No `lando destroy --all`.
- No `curl ... | bash`, `wget ... | sh` (arbitrary-script piping).

## What's NOT denied (intentional)

Things agents need for normal development, with deliberate safety limits:

- **`git push --force-with-lease origin feature/...`** — required for
  rebase workflows. Lease check prevents clobbering concurrent work.
- **`gh pr merge <N> --rebase --delete-branch`** — required for the PR
  merge automation in the `/loop` review flow.
- **`gh pr create`, `gh issue ...`, `gh workflow run`** — normal PR and CI
  plumbing.
- **`go`, `gofmt`, `go test`, `golangci-lint`** — everything in the
  Makefile and `.lando.yml` tooling block.

## Agent boundaries

The project agents (`agents/go-developer.md`, `agents/go-unit-tester.md`)
also encode behavioral constraints:
- No `Co-Authored-By: Claude` trailers on commits.
- Every new exported function takes `context.Context` first.
- Every new exported function gets a matching `Test*` (enforced by
  `scripts/check-test-coverage.sh`).
- Tests use stdlib `testing` only — no testify/gomega/assert libs.
