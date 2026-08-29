// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"os"
	"testing"
)

// postInitBlocksListType captures `blocks list --type`'s backing variable
// exactly once, after every package init() has run and before any test has
// had a chance to reset it.
//
// This snapshot is the ONLY place the issue-#88 aliasing bug is observable.
// pflag writes a flag's default into its bound variable at registration
// time, so the corruption exists from init() onward — and any test that
// zeroes the variable before driving the command (as the end-to-end guard
// must, to stay hermetic) erases the very evidence it means to check.
// resetGlobalOutputFlags now clears it for every test, which closes that
// door for good.
var postInitBlocksListType string

func TestMain(m *testing.M) {
	postInitBlocksListType = blocksListType
	os.Exit(m.Run())
}

// TestBlocksListTypeDefaultAtInit is the registration-time half of the #88
// guard. TestBlocksListTypeNotAliased proves the two flags do not share
// storage; this proves the surviving variable actually starts empty, which
// is what makes an unfiltered `blocks list` return every block type.
func TestBlocksListTypeDefaultAtInit(t *testing.T) {
	if postInitBlocksListType != "" {
		t.Fatalf("blocksListType = %q after package init, want empty — "+
			"`blocks list` would filter every listing to that type with no error",
			postInitBlocksListType)
	}
}
