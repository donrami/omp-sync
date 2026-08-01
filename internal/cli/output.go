// Package cli output helpers.
package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// PrintJSON writes v as indented JSON to w. Errors are returned to the caller.
func PrintJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
}

// PrintHuman writes a one-line human-readable summary to w.
func PrintHuman(w io.Writer, format string, args ...any) error {
	_, err := fmt.Fprintf(w, format+"\n", args...)
	return err
}

// Emit writes v as JSON if --json is set, otherwise as a human-readable
// summary using summarize when --json is off.
func Emit(w io.Writer, jsonMode bool, v any, summarize func(w io.Writer) error) error {
	if jsonMode {
		return PrintJSON(w, v)
	}
	return summarize(w)
}

// KV is a small helper for human-readable "key: value" lines.
type KV struct {
	K string
	V any
}

// PrintKV prints aligned key:value lines.
func PrintKV(w io.Writer, kvs []KV) error {
	max := 0
	for _, kv := range kvs {
		if len(kv.K) > max {
			max = len(kv.K)
		}
	}
	for _, kv := range kvs {
		if _, err := fmt.Fprintf(w, "  %-*s  %v\n", max, kv.K, kv.V); err != nil {
			return err
		}
	}
	return nil
}
