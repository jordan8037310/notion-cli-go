/*
Copyright © 2023 NAME HERE <EMAIL ADDRESS>
*/
package main

import (
	"notioncli/cmd"
)

// version is the notioncli release identifier surfaced by the `version`
// subcommand. The default "dev" sentinel keeps `go install` and unflagged
// `go build` invocations honest — both produce a binary that reports its
// build provenance without needing release plumbing.
//
// Release builds override this at link time:
//
//	go build -ldflags "-X main.version=v0.1.0" .
//
// The release workflow (.github/workflows/release.yml) injects the tag
// name (e.g. v0.1.0) so users running `notioncli version` on a tagged
// binary see the exact tag that produced it. The homebrew formula in the
// companion tap repo passes the same ldflag via std_go_args.
var version = "dev"

func main() {
	cmd.SetVersion(version)
	cmd.Execute()
}
