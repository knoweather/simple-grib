//go:build js && wasm

// gribwasm 把 simple-grib 编译成 WebAssembly 模块，供浏览器 / Node 直接解码
// GRIB 字节流（GRIB1 与 GRIB2）。模块本身不碰 DOM，只在 globalThis 上
// 挂一个 `simpleGrib` 对象：
//
//	simpleGrib.version()                 -> "0.1.0"
//	simpleGrib.decode(bytes, opts)       -> 解析结果（含 Float32Array 数值）
//	simpleGrib.metadata(bytes, opts)     -> 只出元数据，不解码数值（快）
//
// 设计取舍：核心逻辑（decodeBytes）与 JS 绑定（api.go）分开，前者是纯 Go、
// 可在 wasm 下直接单测；后者只负责 js.Value <-> Go 的类型搬运。
package main

import (
	"fmt"
	"syscall/js"
)

func main() {
	register(js.Global())
	logf("simple-grib wasm v%s ready", Version)
	// 保持 Go runtime 存活，否则 main 一返回模块就被回收、回调失效。
	select {}
}

// logf 走宿主 console.log；宿主没有 console 时静默忽略（不影响解码）。
func logf(format string, args ...any) {
	c := js.Global().Get("console")
	if c.Type() != js.TypeObject {
		return
	}
	c.Call("log", fmt.Sprintf(format, args...))
}
