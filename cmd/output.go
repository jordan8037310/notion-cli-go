// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

// globalJSON and globalPretty back the persistent --json and --pretty flags
// wired on rootCmd. They are package-level vars so every subcommand can
// consult them from its Run/RunE without drilling the *cobra.Command.
//
// Design note: every subcommand should branch on globalJSON early and emit
// JSON via the helpers below, or fall through to the existing human output
// path. Per the --json precedent set by #20 (search), follow this split:
//
//   - Heterogeneous endpoints (search, page/database fetches whose full
//     shape the CLI does not fully model) prefer a Raw json.RawMessage
//     pass-through so unknown fields survive the round-trip.
//   - Stable typed responses (blocks list, comment list, user list, todo
//     list, etc.) use plain json.Encoder.Encode on the typed struct.
var (
	globalJSON   bool
	globalPretty bool
)

// globalOutput backs --output=text|json and is a string alias for the
// boolean --json flag so scripts can choose either form. The PreRunE
// normalises "json" -> globalJSON = true.
var globalOutput string

// emitJSON encodes v as a JSON document to w and terminates the line with
// a newline. When globalPretty is set the encoder uses a two-space indent;
// otherwise the encoder's default compact form is used.
//
// Note: json.Encoder.Encode writes a trailing newline itself, so callers
// should not append one.
func emitJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	if globalPretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("emit json: %w", err)
	}
	return nil
}

// emitList is the canonical emitter for list commands (blocks list,
// databases query, users list, teams list, comments list, list, search,
// etc.). It picks the output shape based on globalPretty:
//
//   - globalPretty == false → NDJSON: one compact JSON object per line.
//     This is the pipe-friendly shape and the default for --json on a
//     list command. `jq -c` consumers should use this.
//
//   - globalPretty == true  → a single indented JSON array containing all
//     elements. This is the human-inspection shape. Conventionally jq, gh,
//     and friends do the same: compact NDJSON for piping, pretty array
//     for eyeballing. Indented NDJSON would be broken (each record spans
//     multiple lines) so we deliberately do not offer that combination.
//
// items must be a slice. Anything else is a programmer error and returns
// a descriptive error rather than panicking.
func emitList(w io.Writer, items interface{}) error {
	// Reflect over a generic []T so every call site can pass the typed
	// slice it already has without a pre-conversion step. This only runs
	// during output, so the reflect cost is negligible.
	rv := reflect.ValueOf(items)
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return fmt.Errorf("emit list: want slice, got %T", items)
	}
	if globalPretty {
		// Single pretty-printed JSON array. emitJSON handles the indent
		// and trailing newline.
		return emitJSON(w, items)
	}
	// Compact NDJSON. Force the per-element encoder into compact mode by
	// calling json.Encoder directly (ignoring globalPretty, which is
	// false here anyway) so this path stays valid NDJSON no matter what
	// global state callers might have flipped.
	enc := json.NewEncoder(w)
	for i := 0; i < rv.Len(); i++ {
		if err := enc.Encode(rv.Index(i).Interface()); err != nil {
			return fmt.Errorf("emit list: %w", err)
		}
	}
	return nil
}

// emitError writes a single-line JSON error object to w. Always called on
// cmd.ErrOrStderr() so the stdout stream stays a valid NDJSON pipe for
// jq consumers even when something fails. The encoder's trailing
// newline terminates the line.
func emitError(w io.Writer, err error) {
	if err == nil {
		return
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(map[string]string{"error": err.Error()})
}

// jsonErrorOr is the RunE error-wrap helper. Usage:
//
//	return jsonErrorOr(cmd, err)
//
// When globalJSON is set and err is non-nil, a one-line JSON error object
// is emitted to cmd.ErrOrStderr() before the error is returned so cobra
// still sets a non-zero exit code. The helper is a no-op when err is
// nil, so it is safe to wrap every terminal return in a RunE.
func jsonErrorOr(cmd *cobra.Command, err error) error {
	if err != nil && globalJSON {
		emitError(cmd.ErrOrStderr(), err)
	}
	return err
}

// emitOK emits a minimal success envelope for commands that have no
// natural result payload (delete, archive, unarchive, check, uncheck).
// The envelope is {"ok":true, ...extras} so consumers can grep or jq on
// the shape without special-casing empty bodies.
func emitOK(w io.Writer, extras map[string]interface{}) error {
	obj := map[string]interface{}{"ok": true}
	for k, v := range extras {
		obj[k] = v
	}
	return emitJSON(w, obj)
}

// disableColor turns off fatih/color ANSI escapes globally. Called from
// the rootCmd PersistentPreRunE whenever --json (or --output=json) is on
// so commands that still call color.Green / color.Red in their output
// path do not bleed escape codes into the JSON stream.
func disableColor() {
	color.NoColor = true
}

// applyGlobalOutput normalises the --output=text|json alias into the
// globalJSON boolean. Invalid values surface an error so typos do not
// silently degrade to text output. Empty --output leaves globalJSON
// alone (the --json flag still controls).
func applyGlobalOutput() error {
	switch globalOutput {
	case "":
		return nil
	case "text":
		globalJSON = false
		return nil
	case "json":
		globalJSON = true
		return nil
	}
	return fmt.Errorf("invalid --output %q (want text|json)", globalOutput)
}

// resetGlobalOutputFlags is used by tests to return to the default
// state between runs. cobra retains bound flag values across the
// process so this reset is required to keep tests hermetic.
func resetGlobalOutputFlags() {
	globalJSON = false
	globalPretty = false
	globalOutput = ""
	globalPage = ""
	aliasStoreOverride = nil
}
