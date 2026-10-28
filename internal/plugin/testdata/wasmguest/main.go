//go:build wasip1

// Command wasmguest is a plugin API 2 WebAssembly plugin for the tests: it
// probes what a module may touch and calls Hive through the host module.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"unsafe"
)

//go:wasmimport hive call
func hiveCall(ptr unsafe.Pointer, size uint32) uint32

//go:wasmimport hive response
func hiveResponse(ptr unsafe.Pointer)

// call performs a Hive method and returns the response's JSON.
func call(method string, params any) string {
	req, _ := json.Marshal(map[string]any{"method": method, "params": params})
	n := hiveCall(unsafe.Pointer(&req[0]), uint32(len(req)))
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	hiveResponse(unsafe.Pointer(&buf[0]))
	return string(buf)
}

func main() {
	switch os.Args[1] {
	case "probe":
		stdin, _ := io.ReadAll(os.Stdin)
		var ctx struct {
			Plugin string `json:"plugin"`
		}
		_ = json.Unmarshal(stdin, &ctx)
		fmt.Println("context:", ctx.Plugin)
		fmt.Println("data:", os.Getenv("HIVE_PLUGIN_DATA"))
		fmt.Println("write_data:", os.WriteFile("/data/written", []byte("x"), 0o644) == nil)
		_, err := os.ReadFile("/etc/passwd")
		fmt.Println("read_host:", err == nil)
		fmt.Println("allowed:", call("plugin.capabilities", nil))
		fmt.Println("denied:", call("pane.list", map[string]any{}))
	case "exit":
		os.Exit(3)
	case "spin":
		for {
		}
	}
}
