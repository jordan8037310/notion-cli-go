# Notion CLI Go

 
![Notion CLI Go](notioncli.gif)


Notion CLI Go is a command-line interface tool written in Go to manage tasks in Notion.so. This tool is a new iteration based on the original [Python project](https://github.com/kris-hansen/notion-cli), now built in Go for improved performance and portability. This project is a work in progress, some of the features mentioned below are aspirational and not yet implemented.

## Installation

To install the project, you'll need to clone the repository and build the Go application. Here are the basic instructions:

```bash
git clone https://github.com/kris-hansen/notion-cli-go
cd notion-cli-go
go build
go install .
```

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
# List all blocks
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

### Other Commands

- `completion`: Generate the autocompletion script for your shell
- `help`: Show help information.

## Known Limitations

Currently, the tool only supports a single Notion page at a time.

## Testing

Currently some of the utility package block functions have test coverage. Invoke the tests with:

```bash
go test -v ./...
```

## License

This project is licensed under the Apache License 2.0. See the LICENSE file for details.

