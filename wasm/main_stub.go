//go:build !js || !wasm

// 这个目录是 simple-grib 的 WebAssembly 入口。它只在 js/wasm 目标下有内容：
//
//	GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -o grib.wasm ./wasm
//
// 在其它平台上构建只会得到这个空壳 main，作用是让 `go build ./...`
// 在任何平台都保持干净（不出现 "build constraints exclude all Go files"）。
// 详见 wasm/README.md。
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "gribwasm 只能在 js/wasm 下构建：GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -o grib.wasm ./wasm")
}
