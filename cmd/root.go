// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"notioncli/utils"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "notioncli",
	Short: "Notioncli provides a CLI interface to track your tasks in a Notion page",
	Long: `Notioncli is a tool that utilizes the official Notion API to enable the integration of to-do lists from Notion pages into your command line interface.

		This version supports the following options:
		  list (to list tasks)
		  add <task> (create a new task)
		  check <number> (mark a task done)
		  uncheck <number> (mark a task as not done)
		  delete <number> (permanently remove a task)
		  help (get some help)

		Targeting a page:
		  Use --page <id|alias> on page-scoped commands to override the
		  default. Aliases live in ~/.config/notioncli/pages.yaml and are
		  managed with 'notioncli pages add-alias' and 'pages list-aliases'.
		  When --page is absent, NOTION_PAGE_ID is still honored for back-
		  compat. Resolution order: --page > NOTION_PAGE_ID > error.`,
	// PersistentPreRunE fires for every subcommand. It normalises the
	// --output=text|json alias into the boolean flag and turns off ANSI
	// color output whenever JSON is on so downstream commands that still
	// call color.* cannot bleed escape codes into the JSON stream.
	//
	// In JSON mode it also silences cobra's own error/usage printing.
	// jsonErrorOr already writes a one-line JSON error envelope to
	// stderr; without this, cobra appended its plain-text "Error: ..."
	// (and the usage block) to the same stream, so a JSON-mode failure
	// emitted two outputs for one error and broke any consumer treating
	// stderr as line-delimited JSON — issue #64.
	//
	// The flags go on the root command, not on the leaf being executed.
	// (cmd.Root() rather than the rootCmd package var, which would be an
	// initialization cycle inside its own literal.) Cobra
	// checks both (`!cmd.SilenceErrors && !root.SilenceErrors`), so
	// rootCmd alone is sufficient — and it leaves alone the commands
	// that set these declaratively for their own reasons (searchCmd) or
	// inside their RunE (the blocks subcommands). Mutating the leaf
	// would also strand it silenced for the rest of the process, since
	// nothing resets it.
	//
	// Text mode is untouched: cobra keeps printing "Error: ..." plus
	// usage exactly as before. resetGlobalOutputFlags restores these so
	// in-process reuse (tests) stays hermetic.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := applyGlobalOutput(); err != nil {
			return err
		}
		if globalJSON {
			disableColor()
			root := cmd.Root()
			root.SilenceErrors = true
			root.SilenceUsage = true
		}
		return validateWatchFlag(cmd)
	},
}

// osExit is indirected through a package-level var so tests can swap in a
// no-op and assert on the exit decision without terminating the test binary.
var osExit = os.Exit

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
//
// The banner line is suppressed when --json (or --output=json) is set so
// consumers piping stdout into jq get a valid NDJSON stream. When --help
// is requested we also skip the banner since the help formatter already
// prints a short header.
func Execute() {
	if !shouldSuppressBanner() {
		boldBlue := color.New(color.Bold, color.FgBlue).SprintFunc()
		fmt.Println(boldBlue("----=[ NotionCLI ]=----"))
	}
	err := rootCmd.Execute()
	if err != nil {
		// JSON mode silences cobra's own error printing (issue #64) so a
		// failure emits exactly one line: jsonErrorOr's envelope. But not
		// every RunE routes its error through jsonErrorOr — several
		// validation paths (cmd/views.go among them) return a bare error.
		// Without this backstop those failures printed nothing at all on
		// either stream and exited 1, which is strictly worse than the
		// double-print #64 set out to fix. Emit here when nothing else
		// did, so "exactly one line" holds without requiring every call
		// site to remember the helper.
		if globalJSON && !jsonErrorEmitted {
			emitError(rootCmd.ErrOrStderr(), err)
		}
		osExit(1)
	}
}

// shouldSuppressBanner returns true when the invocation should skip the
// cosmetic banner line. We look at os.Args directly (rather than after
// cobra parses the flags) so the very first byte of stdout is not a
// terminal escape. cobra's Execute() is what wires --json into the
// globalJSON var, and by then the banner has already been written.
//
// We suppress only when JSON is definitely on. That means:
//   - bare --json
//   - --output=json (single-token form)
//   - --output json (space-separated form) — we peek the next arg
//
// --output=text (or --output text) must NOT suppress the banner so human
// invocations keep the banner they always had.
func shouldSuppressBanner() bool {
	args := os.Args[1:]

	// `pages markdown` emits a document, not a report. Its whole purpose is
	// `notioncli pages markdown <id> > page.md`, and a cosmetic banner on
	// the first line corrupts that file — the same class of problem as the
	// banner in front of a JSON payload (#67).
	//
	// `pages export` is the same shape: its default --format json writes
	// one JSON document to stdout, so the banner made `pages export <id> |
	// jq` fail on the very first byte. Suppressed for every format rather
	// than only json — tree is an outline someone pipes to a pager, and md
	// prints one summary line that reads fine without a banner over it.
	if len(args) >= 2 && args[0] == "pages" && (args[1] == "markdown" || args[1] == "export") {
		return true
	}

	for i, a := range args {
		switch {
		case a == "--json", a == "--output=json":
			return true
		case strings.HasPrefix(a, "--json="):
			// Cobra accepts the explicit boolean form for bool flags
			// (--json=true / --json=1 / --json=false). Without this
			// branch, --json=true would slip past the matcher and the
			// banner would print before the JSON payload, breaking
			// every machine consumer (#67). Mirror cobra's truthiness
			// rules via strconv.ParseBool so the matcher accepts the
			// same set of values cobra will: 1, t, T, TRUE, true, True
			// (and the corresponding false set, which we want to
			// reject so explicit-off keeps the banner).
			if v, err := strconv.ParseBool(strings.TrimPrefix(a, "--json=")); err == nil && v {
				return true
			}
		case a == "--output":
			if i+1 < len(args) && args[i+1] == "json" {
				return true
			}
		}
	}
	return false
}

// globalPage backs the persistent --page flag on rootCmd. It holds either
// a raw Notion page id or an alias name; resolvePageID translates aliases
// via utils.AliasStore at invocation time, not at flag-parse time, so a
// typo in an alias is reported by the specific command that needed it.
var globalPage string

// globalResolveMentions backs the persistent --resolve-mentions flag on
// rootCmd. Default off. When true, mention-bearing commands (today:
// blocks list, human-output path only) build a CachingPageResolver and
// pass it into the rich-text renderer so "[page:<id>]" expands to
// "[<title>]". One API call per unique page id per invocation —
// see utils.CachingPageResolver for the caching contract.
//
// JSON output paths intentionally ignore this flag: expanding mentions
// there would be lossy round-tripping (the original rich_text mention
// shape is replaced by a bracketed string). This is a human-rendering
// affordance only.
var globalResolveMentions bool

// aliasStoreOverride is a test seam: if non-nil, resolvePageID uses it
// instead of utils.DefaultAliasStore(). Production code leaves it nil so
// the real ~/.config/notioncli/pages.yaml path is consulted.
var aliasStoreOverride *utils.AliasStore

// resolvePageID returns the page id a command should operate on. The
// resolution order is fixed and documented in rootCmd.Long:
//
//  1. --page flag (literal id or alias). Aliases are resolved via
//     utils.AliasStore; unknown aliases surface an error citing the name.
//  2. NOTION_PAGE_ID environment variable (legacy, still supported).
//  3. Error — neither source produced a page id.
//
// The helper itself does not touch the filesystem unless --page was given
// and is non-uuid-shaped, so the NOTION_PAGE_ID-only path keeps its
// original behaviour (no new I/O, no new error modes).
func resolvePageID() (string, error) {
	if globalPage != "" {
		store := aliasStoreOverride
		if store == nil {
			s, err := utils.DefaultAliasStore()
			if err != nil {
				return "", fmt.Errorf("resolve --page: %w", err)
			}
			store = s
		}
		id, err := store.Resolve(globalPage)
		if err != nil {
			return "", fmt.Errorf("resolve --page: %w", err)
		}
		return id, nil
	}
	if env := os.Getenv("NOTION_PAGE_ID"); env != "" {
		return env, nil
	}
	return "", fmt.Errorf("no target page: set --page <id|alias> or NOTION_PAGE_ID")
}

func init() {
	// Persistent output flags. Every subcommand inherits these.
	rootCmd.PersistentFlags().BoolVar(&globalJSON, "json", false, "Emit JSON/NDJSON to stdout (disables ANSI color)")
	rootCmd.PersistentFlags().BoolVar(&globalPretty, "pretty", false, "Pretty-print JSON output (list commands emit a single indented JSON array; compact NDJSON is recommended for piping)")
	rootCmd.PersistentFlags().StringVar(&globalOutput, "output", "", "Output format: text|json (alias for --json)")

	// Re-poll a list command until interrupted. Persistent so the flag is
	// spelled the same everywhere, with PersistentPreRunE rejecting it on
	// the commands that do not loop rather than ignoring it there.
	rootCmd.PersistentFlags().StringVar(&globalWatch, "watch", "",
		"Re-run a list command every interval (e.g. 30s, 5m) until interrupted")

	// Persistent page-targeting flag. resolvePageID translates aliases
	// lazily so unknown aliases surface at command run time, not here.
	rootCmd.PersistentFlags().StringVar(&globalPage, "page", "", "page id or alias; falls back to NOTION_PAGE_ID env var")

	// Opt-in page-mention resolution. Default off so the existing
	// "[page:<id>]" rendering stays the baseline. When set, commands
	// that surface rich text to humans build a CachingPageResolver
	// (one API call per unique page id) and expand page mentions to
	// "[<title>]". Ignored on --json paths.
	rootCmd.PersistentFlags().BoolVar(&globalResolveMentions, "resolve-mentions", false,
		"resolve page mentions from [page:<id>] to [<title>] (issues one API call per unique page; human output only)")
}
