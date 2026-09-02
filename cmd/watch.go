// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// globalWatch backs the persistent --watch flag. It holds the raw string
// so an unparseable value is reported by the command that needed it,
// naming the flag, rather than by cobra's own type error.
var globalWatch string

// watchAnnotation marks a command as safe to re-run on a timer. Commands
// are marked by watchable(), which is also what installs the loop — one
// call does both, so the marker and the behaviour cannot drift apart.
const watchAnnotation = "notioncli/watch"

// watchMinInterval is the floor on --watch. Sub-second polling is a
// non-goal: Notion rate-limits at a few requests per second, so a faster
// loop buys nothing and spends the caller's quota. It is a var only so
// tests can drive the loop without sleeping for real.
var watchMinInterval = time.Second

// watchWait blocks for d or until ctx is done, returning non-nil when the
// watch should stop. Indirected through a var so tests can end the loop
// deterministically instead of racing a timer.
var watchWait = func(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// watchable installs the --watch loop on a command and marks it as
// supporting the flag.
//
// The RunE is wrapped at registration rather than each command calling a
// helper inside its own body. That keeps the five list commands untouched,
// and — more importantly — means the capability annotation and the loop
// are installed by the same call, so a command can never advertise --watch
// without implementing it, or vice versa.
//
// With --watch unset the wrapper calls straight through, so a normal
// invocation behaves exactly as it did before.
func watchable(cmd *cobra.Command) *cobra.Command {
	inner := cmd.RunE
	if inner == nil {
		// A Run (non-E) command would silently never loop. Nothing in the
		// tree is shaped that way today; panic rather than ship a flag
		// that quietly does nothing if one appears.
		panic("watchable: " + cmd.Name() + " has no RunE")
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[watchAnnotation] = "yes"
	cmd.RunE = func(c *cobra.Command, args []string) error {
		return watchLoop(c, args, inner)
	}
	return cmd
}

// watchLoop re-runs a command until interrupted.
//
// A failing iteration ENDS the watch rather than being swallowed and
// retried. The transport already retries the failures worth retrying
// (429, 5xx on idempotent verbs — see utils/retry.go), so an error that
// reaches here is one more polling will not fix: a revoked token, a
// deleted page, a malformed filter. Looping on it would print the same
// error forever and mask it behind a wall of output.
func watchLoop(cmd *cobra.Command, args []string, run func(*cobra.Command, []string) error) error {
	if globalWatch == "" {
		return run(cmd, args)
	}
	interval, err := parseWatchInterval(globalWatch)
	if err != nil {
		return jsonErrorOr(cmd, err)
	}

	// SIGINT and SIGTERM end the watch cleanly, with exit 0: the loop was
	// asked to stop, which is not a failure. The context is handed to the
	// command so a fetch already in flight can be cancelled too.
	// cmd.Context() is nil until cobra's Execute sets it, and
	// signal.NotifyContext panics on a nil parent. Production always goes
	// through Execute, but a nil-parent panic is a poor way to find that
	// out if a caller ever does not.
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd.SetContext(ctx)

	out := cmd.OutOrStdout()
	tty := isTerminal(out)
	for iteration := 0; ; iteration++ {
		switch {
		case tty:
			// Redraw in place: the point of a TTY watch is a live view,
			// not a growing scrollback.
			fmt.Fprint(out, "\033[H\033[2J")
		case iteration > 0 && !globalJSON:
			// Piped and human-readable: mark where each render begins so
			// the accumulated output stays readable. Deliberately NOT
			// written in JSON mode — a separator would make the stream
			// invalid NDJSON, and `--json --watch` exists precisely to be
			// piped into jq.
			fmt.Fprintf(out, "\n--- %s ---\n", time.Now().Format(time.RFC3339))
		}
		if err := run(cmd, args); err != nil {
			return err
		}
		if err := watchWait(ctx, interval); err != nil {
			return nil
		}
	}
}

// parseWatchInterval validates the --watch value.
func parseWatchInterval(raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("--watch %q is not a duration: use a form like 30s, 5m or 1h", raw)
	}
	if d < watchMinInterval {
		return 0, fmt.Errorf("--watch %s is below the %s minimum: Notion rate-limits at a few requests per second, so polling faster returns the same data and spends your quota",
			raw, watchMinInterval)
	}
	return d, nil
}

// isTerminal reports whether w is an interactive terminal. A command whose
// output has been redirected to a file or a pipe must not emit cursor
// escapes into it.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// validateWatchFlag checks --watch before the command does any work, and
// reports the failure itself.
//
// It has to print rather than leave it to cobra because some commands set
// SilenceErrors for their own reasons (searchCmd does). Under those, a bad
// --watch exited 1 having written nothing at all to either stream — the
// same silent-failure shape as #64. Printing here and silencing the root
// afterwards means exactly one message, from one place, whatever the leaf
// command has configured. It also drops the usage block, which is noise
// when the problem is one flag's value.
func validateWatchFlag(cmd *cobra.Command) error {
	if globalWatch == "" {
		return nil
	}
	err := rejectUnsupportedWatch(cmd)
	if err == nil {
		_, err = parseWatchInterval(globalWatch)
	}
	if err == nil {
		return nil
	}
	if globalJSON {
		emitError(cmd.ErrOrStderr(), err)
	} else {
		errorLine(cmd, "Error: %v", err)
	}
	root := cmd.Root()
	root.SilenceErrors = true
	root.SilenceUsage = true
	return err
}

// rejectUnsupportedWatch fails a --watch invocation on a command that does
// not support it.
//
// Silently ignoring the flag is the wrong failure: the caller is watching
// their terminal expecting it to refresh, and nothing ever happens. The
// error names the commands that do support it so the next attempt works.
func rejectUnsupportedWatch(cmd *cobra.Command) error {
	if globalWatch == "" || cmd.Annotations[watchAnnotation] != "" {
		return nil
	}
	return fmt.Errorf("--watch does not apply to %q; it is supported on: %s",
		cmd.CommandPath(), strings.Join(watchableCommands(cmd.Root()), ", "))
}

// watchableCommands lists the command paths that accept --watch, so the
// error above stays correct as commands are added or removed.
func watchableCommands(root *cobra.Command) []string {
	var out []string
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Annotations[watchAnnotation] != "" {
			out = append(out, c.CommandPath())
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	sort.Strings(out)
	return out
}
