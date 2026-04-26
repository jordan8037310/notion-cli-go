// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// versionString is the release identifier surfaced by the `version`
// subcommand. main.go injects the value via SetVersion at process start.
// The "dev" sentinel keeps unflagged `go build` and `go install` honest:
// developers running an unreleased binary see "dev" rather than a stale
// hard-coded tag. Release builds override main.version at link time, and
// main calls SetVersion(version) before cobra dispatch — see main.go.
var versionString = "dev"

// SetVersion lets main inject the link-time-resolved version string into
// the cmd package without exposing the underlying var. Calling it after
// rootCmd.Execute() has no effect on already-running invocations, so
// main.go MUST call this before Execute().
func SetVersion(v string) {
	if v != "" {
		versionString = v
	}
}

// versionCmd prints the build version. JSON mode emits a single-key object
// so scripted consumers can `notioncli version --json | jq -r .version`
// without parsing free-form text.
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the notioncli build version",
	Long: `Print the notioncli build version.

Release builds embed the git tag (e.g. v0.1.0) via -ldflags. Source
builds report "dev". Use --json for a machine-readable form.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		w := cmd.OutOrStdout()
		if globalJSON {
			enc := json.NewEncoder(w)
			return enc.Encode(map[string]string{"version": versionString})
		}
		fmt.Fprintln(w, versionString)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
