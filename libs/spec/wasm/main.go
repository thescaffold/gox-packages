//go:build js && wasm

// Command wasm is the browser build of spec: it exposes ospecAnalyze(text) to
// JavaScript, returning the JSON of spec.Analyze. It is the same Go code the
// server runs, so there is one parser, not two.
package main

import (
	"syscall/js"

	"github.com/thescaffold/gox-packages/libs/spec"
)

func main() {
	js.Global().Set("ospecAnalyze", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) < 1 {
			return js.Null()
		}
		return string(spec.AnalyzeJSON(args[0].String()))
	}))
	// Stay alive so the function can be called.
	select {}
}
