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
// otherwise the encoder's default compact form is used. Callers pass a
// slice for list commands (one document), or call emitJSONLine per
// element for NDJSON.
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

// emitJSONLines encodes every element of items on its own line (NDJSON).
// When globalPretty is set each element is pretty-printed but still
// terminated with a single newline between elements. This is the shape
// list commands (blocks list, databases query, users list, comments
// list, search) use when --json is on.
func emitJSONLines(w io.Writer, items interface{}) error {
	// Reflect over a generic []T so every call site can pass the typed
	// slice it already has without a pre-conversion step. This is only
	// called during output, so the reflect cost is negligible.
	rv := reflect.ValueOf(items)
	if !rv.IsValid() || rv.Kind() != reflect.Slice {
		return fmt.Errorf("emit json lines: want slice, got %T", items)
	}
	for i := 0; i < rv.Len(); i++ {
		if err := emitJSON(w, rv.Index(i).Interface()); err != nil {
			return err
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
}
