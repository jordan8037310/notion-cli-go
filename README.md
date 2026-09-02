# Notion CLI Go

 
![Notion CLI Go](notioncli.gif)


Notion CLI Go is a command-line interface tool written in Go to manage tasks in Notion.so. This tool is a new iteration based on the original [Python project](https://github.com/kris-hansen/notion-cli), now built in Go for improved performance and portability. This project is a work in progress, some of the features mentioned below are aspirational and not yet implemented.

## Install

The fastest path is the homebrew tap (macOS + linuxbrew):

```bash
brew install jordan8037310/notioncli/notioncli
```

> The tap repo (`jordan8037310/homebrew-notioncli`) is published separately. If `brew tap` reports the tap is missing, fall back to one of the manual options below until the tap is live.

**Manual via `go install`** (any platform with Go 1.21+):

```bash
go install github.com/jordan8037310/notion-cli-go@latest
# binary lands in $(go env GOPATH)/bin as `notion-cli-go`; rename or symlink to `notioncli` if you like
```

**From source** (clone + build):

```bash
git clone https://github.com/jordan8037310/notion-cli-go
cd notion-cli-go
go build -o notioncli .
```

Confirm the install with:

```bash
notioncli version
```

Tagged releases are published at <https://github.com/jordan8037310/notion-cli-go/releases> with prebuilt darwin/linux binaries (amd64 + arm64) and per-asset sha256 sidecars.

## Configuration

Before running the notioncli tool, you need to set the following environment variables:

- `NOTION_API_KEY`: Your Notion Official API key.
- `NOTION_PAGE_ID`: The URL for the page (ex: https://notion.so/my-page/{pageID}). Here are some [tips](https://developers.notion.com/docs/working-with-page-content#:~:text=Open%20the%20page%20in%20Notion,ends%20in%20a%20page%20ID.) for finding your page ID. You will also need to share this page as an integration to expose it to the cli tool. Keep in mind that the page ID in the URL will have the title as `Title-<PageID>` and the title portion will need to be removed.
- `LOCAL_TIMEZONE`: Your local timezone (ex: 'America/New_York').

For the `NOTION_API_KEY`, visit [Notion's integration page](https://www.notion.so/my-integrations) and create a new integration. Remember to share your task page with the integration.

Your `.env` file can either be located in your working directory or in `~/.config/notioncli/.env` - for convenience there is a sample env file named `.env.example` 
## Usage

You can interact with the tool using the built binary:

```bash
./notioncli [command]
```

### Task Commands (To-Do focused)

- `list`: List all to-do tasks on the Notion page.
- `add <task>`: Add a new to-do task to the Notion page.
- `check <number>`: Mark a task as complete.
- `uncheck <number>`: Mark a task as incomplete.
- `delete <number>`: Delete a to-do task from the Notion page.

### Block Commands (All block types)

The `blocks` subcommand allows you to work with all Notion block types:

```bash
# List all blocks (top level only)
notioncli blocks list

# List only specific block types
notioncli blocks list --type heading_1

# Add different block types
notioncli blocks add "Hello world"                    # paragraph (default)
notioncli blocks add "Section Title" -t heading_1     # heading
notioncli blocks add "Buy milk" -t to_do              # to-do item
notioncli blocks add "Important note" -t callout      # callout
notioncli blocks add "" -t divider                    # divider

# Delete any block by index
notioncli blocks delete 5
```

**Supported block types:**
- `paragraph` - Regular text
- `heading_1`, `heading_2`, `heading_3` - Headings
- `bulleted_list_item`, `numbered_list_item` - List items
- `to_do` - Checkbox items
- `toggle` - Collapsible content
- `quote` - Block quotes
- `callout` - Highlighted callouts
- `divider` - Horizontal dividers
- `code` - Code blocks

### Page Commands

- `pages get <id>`: Retrieve a page. Warns when a property has hit Notion's
  25-reference cap and may be truncated.
- `pages property <page-id> <property-id>`: Read one property in full, past that
  cap. Property ids come from `pages get <id> --json`.
- `pages markdown <page-id>`: Render a whole page as markdown, **including nested
  content**. The only single command that returns a page's complete text —
  `blocks list` is top-level only. Warns on stderr if Notion truncated the render
  or could not handle particular blocks, so `pages markdown <id> > page.md`
  produces a clean file.
- `pages create`: Create a page under a page or database parent.
- `pages update <id>`: Update a page's title or properties.
- `pages archive <id>` / `pages unarchive <id>`: Move a page to or from the trash.
- `pages move <id> --parent <id>`: Reparent a page.
- `pages duplicate <id> --parent <id>`: Copy a page and its blocks.
- `pages set-icon <id> <path>` / `pages set-cover <id> <path>`: Upload a local
  image and set it as the page's icon or cover.
- `pages add-alias <name> <id>` / `pages list-aliases`: Manage the page aliases
  used by `--page`.

### Database Commands

- `databases get <id>`: Retrieve a database.
- `databases data-sources <id>`: List the data sources a database contains.
  Since Notion-Version 2025-09-03 a database is a *container*; it is the data
  source that holds the schema and answers queries, so this is how you find the
  id to query.
- `databases query <id>`: Query a data source, paginating results. Accepts
  `--data-source <id>`, `--filter-json`, `--sort-json` and `--limit`.
- `databases create`: Create a database. `--properties-json` defines the schema for
  its initial data source.
- `databases update <id>`: Update a title, a schema, or both. A schema update is
  applied to the data source — pass either id, a database is resolved
  automatically — and title plus schema travel in a single request.

### Workspace Commands

- `search <query>`: Search pages and databases across the workspace
  (`--type pages|databases`).
- `fetch <url-or-id>`: Fetch a page or database by URL or id, auto-detecting
  which it is.
- `comments list` / `comments create`: Read and post comments.
- `users list` / `users get <id>` / `users whoami`: Workspace users and the
  current integration.
- `views list <data-source-id>` / `views get <view-id>`: Discover views. The list
  endpoint returns ids only; `views get` shows name, type and config.
- `views create <database-id> --data-source <ds-id>` / `views update <view-id>`:
  Manage data source views. A view reads a data source and belongs to a database
  container, and Notion requires both ids.
- `teams`: Workspace teams (see Known Limitations).

### Reading a whole page

`blocks list` returns **top-level blocks only** — toggles, columns and tables
hide their contents. Two ways to see everything:

```bash
notioncli blocks list --page <id> --recursive   # structured, indented by depth
notioncli pages markdown <id> > page.md         # Notion's own markdown render
```

`--recursive` accepts `--max-depth N` (1 = top level only, 0 = unlimited) and
carries a `depth` field in `--json`.

### Global Flags

- `--page <id|alias>`: Target a specific page. Overrides `NOTION_PAGE_ID`.
- `--json` / `--output json`: Emit JSON. List commands produce NDJSON — one
  object per line — for piping into `jq`.
- `--pretty`: Pretty-print JSON. List commands emit a single indented array
  instead of NDJSON.
- `--resolve-mentions`: Expand page mentions from `[page:<id>]` to `[<title>]`
  in human output. Costs one API call per distinct page, cached per invocation.

### Other Commands

- `version`: Print the build version.
- `completion`: Generate the autocompletion script for your shell.
- `help`: Show help information.

## Known Limitations

- **Teams**: the teams API is unavailable on Notion-Version 2026-03-11; the
  command returns a clear error rather than a raw 400 (issue #37).
- **Retries are conservative on writes by design**: 429 and 529 are always
  retried, but 5xx is retried only for `GET` and `DELETE`. A 502 on a `POST` may
  mean the write *did* land and the gateway lost the response, so replaying it
  could create a duplicate. `PATCH` is excluded for the same reason — appending
  block children is not idempotent. Those failures surface for you to decide on.

Multi-page support landed via `--page` and aliases — the tool is no longer
limited to a single page.

## Testing

```bash
make ci          # everything CI runs: fmt, vet, race tests, coverage, gap gate
go test -race ./...
```

Coverage sits around 87% with a 70% floor enforced by `make cover`.

### Rate limits and retries

Notion rate-limits at roughly 3 requests/second. The client retries
transparently: `Retry-After` is honoured when the server sends it, otherwise
exponential backoff with jitter, capped at 30s and 4 attempts. Backoff is
interruptible — ctrl-C works during a wait.

`4xx` is never retried; a malformed request surfaces immediately rather than
being hammered. See Known Limitations for why writes are treated differently
from reads.

### Running integration tests

The unit suite uses `httptest` mocks, which can only prove the serialiser agrees
with the assumption that produced it. When Notion's `2025-09-03` release moved a
database's schema onto its data source, the mocks kept asserting the old shape
and three commands shipped broken — each returning HTTP 200 while silently
discarding the user's data.

The integration harness exercises the built binary against a real workspace:

```bash
export NOTION_INTEGRATION_API_KEY=secret_...          # a TEST workspace token
export NOTION_INTEGRATION_FIXTURE_PARENT=<page-id>    # a page shared with it
make integration-test
```

Without both variables every test skips with a message, so `go test -tags=integration ./...`
is safe to run anywhere. The variables are deliberately *not* `NOTION_API_KEY`, so an
everyday credential cannot accidentally point a mutating suite at a production workspace.

**Use a dedicated test workspace.** Each run provisions its own scratch page under the
fixture parent, exercises it, and trashes everything it created.

Every request, every CLI invocation and the final page state land in
`integration/.testdata/integration/<run-id>/`, so a failure can be diffed after the fact
rather than only reproduced.

#### What the contracts encode

`integration/contracts_test.go` is a recorded data set, not a set of assumptions — each
claim was observed against a live workspace rather than read from documentation, which
matters because Notion's create-database reference page still carries pre-upgrade prose
that contradicts its own schema.

Half the file is **anti-contracts**: behaviour that is wrong but silent, where Notion
answers `200` and ignores the obsolete key. Those cannot be discovered by a mock, and
every one of them shipped as a green test.

**To move to a new API version:** bump `APIVersion` in `integration/harness.go` and run
the suite. Each failure names the endpoint whose shape moved and prints the body actually
received, so a version bump produces a list of things to fix instead of a production
incident.

## License

This project is licensed under the Apache License 2.0. See the LICENSE file for details.

