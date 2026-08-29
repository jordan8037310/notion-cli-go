# notion-cli-go — Daily Maintenance Loop

You are the daily maintainer for `jordan8037310/notion-cli-go`, a Go CLI that wraps the Notion API. This is an open-source fork of `kris-hansen/notion-cli-go`. The active development branch is `feature/foundation`; PRs target it, not `main`.

Run this loop once per day. Each run is short — investigate, knock out one or two small things, leave the repo in a green state. **Do not** undertake multi-day work in a single run. If a task is bigger than ~2 hours of focused effort, file an issue or PR with a stub and stop.

## Working agreement

- **Branches**: each unit of work goes on its own `feature/<topic>` or `fix/<topic>` branch off the latest `feature/foundation`.
- **PRs target `feature/foundation`** (not `main`). The base for the GitHub Release pipeline is also `feature/foundation` until the eventual `main` cutover.
- **No `Co-Authored-By: Claude` trailer** in commits in this repo (per Jordan's standing instruction).
- **Commit early, commit often** — small, focused commits with descriptive messages.
- **Never push to `upstream/`** — the `kris-hansen` remote is read-only for this fork. Permissions already deny it but don't try to work around.
- **Never force-push to `main`** — protected. Permissions deny it.
- **CI must be green** before merge. `gofmt -l .` clean, `golangci-lint run` clean, `go test -race ./...` green.

## Per-run checklist

Work the list top-down. Stop after the first item that produces a meaningful PR or comment, unless the items are trivially fast (housekeeping). The order reflects priority — silent bugs and broken paths first, polish later.

### 1. Sync local state

```bash
git fetch --all --prune
git switch feature/foundation
git pull --ff-only origin feature/foundation
```

If the local foundation diverges from origin (it shouldn't), report and stop.

### 2. Check upstream for changes worth merging

```bash
git fetch upstream
git log --oneline feature/foundation..upstream/main
git log --oneline origin/main..upstream/main
```

Things to look for in upstream commits:

- Bug fixes to shared code paths (`utils/`, `cmd/`)
- Notion-Version bumps (we pin 2026-03-11; check if upstream advanced)
- Dependency upgrades (`go.mod` / `go.sum`)
- New top-level commands we don't have

If anything matters, summarise it (1–3 sentences per commit) and decide:

- **Cherry-pick worth the conflict cost** → branch off foundation, `git cherry-pick <sha>`, resolve conflicts, open PR titled `chore: cherry-pick upstream <sha>: <subject>`. **Do not** merge `upstream/main` wholesale into foundation — it has 100+ commits of divergent history.
- **Re-implement on our shape** → file an issue describing the upstream change and what we'd want our equivalent to look like. Don't write the code on this run.
- **Skip** (most upstream activity will be irrelevant given how far foundation has diverged).

If upstream has zero new commits, log "upstream clean" and continue.

### 3. Check open PRs on the fork

```bash
gh pr list --repo jordan8037310/notion-cli-go --state open --json number,title,headRefName,statusCheckRollup,mergeStateStatus,reviewDecision
```

For each open PR:

- **CI failing** → look at the failed check (`gh run view <id> --log-failed | tail -100`), fix locally, push to the same branch.
- **CI green + clean** → if it's a small, low-risk PR that's been open >24h with no review, run `/codex:review` and merge with `gh pr merge <n> --merge --delete-branch` if all findings are out of scope or non-blocking. File any out-of-scope findings as new issues.
- **Mergeable but conflicting** → rebase against `feature/foundation`, push.
- **Open but in progress** (today's PR, or has open Codex findings to address) → leave alone.

After any merge, sync local foundation, run `go install .`, and confirm `notioncli version` reports `dev` (the binary is current).

### 4. Triage open issues

```bash
gh issue list --repo jordan8037310/notion-cli-go --state open --json number,title,labels,createdAt
```

Pick **one** issue to act on this run, prioritising in this order:

1. **Silent bugs** (things that fail or behave wrong without an error). Examples: pagination cursor escape (#57 historically), index drift (#55 historically). High blast radius.
2. **User-blocking 400s** from live Notion API (e.g., #37 teams 400, #48 multi-data-source query). Reproduce live first if possible.
3. **Defensive guards** the rest of the codebase already has but one client is missing (typical Codex find).
4. **Small feature requests** that close a parity gap (e.g., extending `--resolve-mentions`, file download).
5. **Larger features** (bulk page creation, recursive export, markdown round-trip) — only if nothing higher-priority is open.

For the chosen issue:

- Branch off the latest foundation
- Implement minimum viable fix + regression test
- Run `gofmt -l .`, `go vet ./...`, `go test -race ./...` — all must be clean
- Commit with `closes #<n>` in the body
- Push and open a PR targeting `feature/foundation`
- Wait for CI; if green, run `/codex:review`. Address blocking findings inline; file out-of-scope findings as new issues; merge if review is clean or only out-of-scope items remain.
- After merge, close the original issue with a reference to the merge commit. (GitHub auto-close only fires when merging to the default branch, which is `main` here, so close manually.)

If you can't reach a clean PR in a single run (e.g., the bug needs investigation against a live API and credentials aren't available), file a comment on the issue with what you found and what's still needed, and stop.

### 5. Light housekeeping (only if nothing above produced work)

- Delete merged remote branches that GitHub didn't auto-clean: `gh api repos/jordan8037310/notion-cli-go/branches | jq -r '.[].name'` against `gh pr list --state merged --json headRefName --jq '.[].headRefName'`.
- Spot-check `~/.claude/projects/-Users-jordanryan-code--testbed-notion-cli-go/memory/MEMORY.md` for stale entries.
- Update `CLAUDE.md` (if any) with anything new and load-bearing learned this week.

Do not "improve" CLAUDE.md, READMEs, or comments without a concrete reason — drift-by-edit costs more than it saves.

## What this loop should never do

- Merge a PR with red CI, even if you "know" the failure is unrelated.
- Push to `main` (protected), `upstream/*` (read-only), or any branch that already has a tag pointing at it.
- Take destructive operations on shared state (delete issues, force-push branches with collaborators, transfer the repo, rotate secrets, etc.) without explicit per-run confirmation from Jordan.
- Run `/codex:review` more than once per PR per session — it costs tokens.
- Schedule additional follow-up loops or cron jobs from inside this loop.

## Quick reference

| Task | Command |
|---|---|
| Pull upstream changes | `git fetch upstream && git log --oneline feature/foundation..upstream/main` |
| List open PRs | `gh pr list --repo jordan8037310/notion-cli-go --state open` |
| List open issues | `gh issue list --repo jordan8037310/notion-cli-go --state open` |
| Run all tests | `go test -race ./...` |
| Format check | `gofmt -l .` |
| Lint | `golangci-lint run` (or via lando: `lando golangci-lint run`) |
| Install local binary | `go install .` |
| Verify binary version | `notioncli version` |
| Open PR | `gh pr create --repo jordan8037310/notion-cli-go --base feature/foundation --head <branch> --title <t> --body <b>` |
| Merge PR | `gh pr merge <n> --repo jordan8037310/notion-cli-go --merge --delete-branch` |
| Close issue with comment | `gh issue close <n> --repo jordan8037310/notion-cli-go --comment "Fixed in PR #<x> (merged at <sha>)."` |

## Reporting

End every run with a short status line summarising what you did:

- "Synced. Upstream clean. CI green on PR #N. Closed #M via PR #X. Filed #Y."
- "Synced. Upstream has 2 new commits — both irrelevant (unrelated demo CLI). No open PRs. Opened PR #X for #N."
- "Synced. Tried to repro #N but ran out of credentials; commented on the issue with the next step."

Keep it under 3 lines. The summary goes to the run log, not a PR comment.
