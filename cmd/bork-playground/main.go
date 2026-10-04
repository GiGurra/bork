//go:build js && wasm

// Command bork-playground exposes compiler operations inside a Web Worker.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/GiGurra/bork/internal/playground"
)

func main() {
	handler := js.FuncOf(func(_ js.Value, args []js.Value) any {
		request := playground.Request{}
		response := playground.Response{}
		if len(args) != 1 || json.Unmarshal([]byte(args[0].String()), &request) != nil {
			response.Error = "invalid playground request"
		} else {
			response = playground.Handle(request)
		}
		data, _ := json.Marshal(response)
		return string(data)
	})
	js.Global().Set("borkPlayground", handler)
	select {}
}
