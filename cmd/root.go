// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"fmt"
	"os"

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
		  help (get some help)`,
	// PersistentPreRunE fires for every subcommand. It normalises the
	// --output=text|json alias into the boolean flag and turns off ANSI
	// color output whenever JSON is on so downstream commands that still
	// call color.* cannot bleed escape codes into the JSON stream.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := applyGlobalOutput(); err != nil {
			return err
		}
		if globalJSON {
			disableColor()
		}
		return nil
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
	for i, a := range args {
		switch a {
		case "--json", "--output=json":
			return true
		case "--output":
			if i+1 < len(args) && args[i+1] == "json" {
				return true
			}
		}
	}
	return false
}

func init() {
	// Persistent output flags. Every subcommand inherits these.
	rootCmd.PersistentFlags().BoolVar(&globalJSON, "json", false, "Emit JSON/NDJSON to stdout (disables ANSI color)")
	rootCmd.PersistentFlags().BoolVar(&globalPretty, "pretty", false, "Pretty-print JSON output (list commands emit a single indented JSON array; compact NDJSON is recommended for piping)")
	rootCmd.PersistentFlags().StringVar(&globalOutput, "output", "", "Output format: text|json (alias for --json)")
}
