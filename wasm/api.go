//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"
	"unsafe"
)

// register 把 simpleGrib 命名空间挂到宿主 globalThis 上。
// 只做 js.Value <-> Go 的类型搬运，业务逻辑全在 decode.go。
func register(g js.Value) {
	api := g.Get("Object").New()
	api.Set("version", js.FuncOf(func(js.Value, []js.Value) any { return Version }))
	api.Set("decode", js.FuncOf(func(_ js.Value, a []js.Value) any { return handle(a, true) }))
	api.Set("metadata", js.FuncOf(func(_ js.Value, a []js.Value) any { return handle(a, false) }))
	g.Set("simpleGrib", api)
}

// handle 是 decode / metadata 的公共入口。第二个参数为 opts 对象，
// 可覆盖默认的 {values, points}。
func handle(a []js.Value, valuesByDefault bool) any {
	if len(a) == 0 {
		return errObject("需要字节参数：Uint8Array / ArrayBuffer / number[]")
	}
	data, err := bytesArg(a[0])
	if err != nil {
		return errObject(err.Error())
	}
	opts := Options{Values: valuesByDefault, Points: true}
	if len(a) > 1 && a[1].Type() == js.TypeObject {
		if v := a[1].Get("values"); v.Type() == js.TypeBoolean {
			opts.Values = v.Bool()
		}
		if v := a[1].Get("points"); v.Type() == js.TypeBoolean {
			opts.Points = v.Bool()
		}
		if v := a[1].Get("perPoint"); v.Type() == js.TypeBoolean {
			opts.PerPoint = v.Bool()
		}
	}
	return toJS(decodeBytes(data, opts))
}

// errObject 返回 {ok:false, errors:[msg]}，与正常返回同构，前端只需看 ok。
func errObject(msg string) js.Value {
	return toJS(&result{OK: false, Messages: []msgResult{}, Errors: []string{msg}})
}

// toJS 把结果对象转成 JS 值。元数据走 JSON（结构清晰、字段可选），
// 大数组走 TypedArray（零字符串化开销）。
func toJS(res *result) js.Value {
	b, err := json.Marshal(res)
	if err != nil {
		// json.Marshal 对本结构不可能失败（全是基础类型），保底不 panic。
		obj := js.Global().Get("Object").New()
		obj.Set("ok", false)
		obj.Set("errors", js.ValueOf([]string{err.Error()}))
		return obj
	}
	obj := js.Global().Get("JSON").Call("parse", string(b))
	msgs := obj.Get("messages")
	for i := range res.Messages {
		item := msgs.Index(i)
		if v := res.Messages[i].Values64; v != nil {
			// GRIB1 原生精度（float64）。
			item.Set("values", float64Array(v))
		} else if v := res.Messages[i].Values; v != nil {
			item.Set("values", float32Array(v))
		}
		if res.Messages[i].Lats != nil {
			item.Get("grid").Set("lats", float64Array(res.Messages[i].Lats))
			item.Get("grid").Set("lons", float64Array(res.Messages[i].Lons))
		}
	}
	return obj
}

// bytesArg 接受 Uint8Array / 任意 TypedArray / ArrayBuffer / number[]，
// 统一复制成 Go 字节切片（复制而非共享，避免 JS 侧复用缓冲区导致数据被改写）。
func bytesArg(v js.Value) ([]byte, error) {
	if v.Type() != js.TypeObject {
		return nil, fmt.Errorf("字节参数必须是对象类型，收到 %s", v.Type())
	}
	arrayBuffer := js.Global().Get("ArrayBuffer")
	uint8Array := js.Global().Get("Uint8Array")

	switch {
	case v.InstanceOf(arrayBuffer):
		return copyFromJS(uint8Array.New(v)), nil
	case v.InstanceOf(uint8Array), v.InstanceOf(js.Global().Get("Uint8ClampedArray")):
		return copyFromJS(v), nil
	case v.Get("buffer").Type() == js.TypeObject && v.Get("buffer").InstanceOf(arrayBuffer):
		// 其它 TypedArray（Int8Array 等）：按底层 buffer 原样重解释为字节。
		view := uint8Array.New(v.Get("buffer"), v.Get("byteOffset"), v.Get("byteLength"))
		return copyFromJS(view), nil
	case v.InstanceOf(js.Global().Get("Array")):
		n := v.Length()
		out := make([]byte, n)
		for i := 0; i < n; i++ {
			out[i] = byte(v.Index(i).Int())
		}
		return out, nil
	}
	return nil, fmt.Errorf("不支持的字节参数类型（需要 Uint8Array / ArrayBuffer / number[]）")
}

func copyFromJS(u8 js.Value) []byte {
	out := make([]byte, u8.Get("byteLength").Int())
	if len(out) > 0 {
		js.CopyBytesToGo(out, u8)
	}
	return out
}

// typedArray 用一个 ArrayBuffer 做桥：CopyBytesToJS 只认 Uint8Array，
// 所以先拷进 Uint8Array 视图，再用同一块 buffer 建出目标 TypedArray。
// wasm 内存是小端，float32/float64 的字节序与 JS 一致。
func typedArray(elem string, n, width int, fill func(dst []byte)) js.Value {
	buf := js.Global().Get("ArrayBuffer").New(n * width)
	if n > 0 {
		dst := make([]byte, n*width)
		fill(dst)
		js.CopyBytesToJS(js.Global().Get("Uint8Array").New(buf), dst)
	}
	return js.Global().Get(elem).New(buf)
}

func float32Array(v []float32) js.Value {
	return typedArray("Float32Array", len(v), 4, func(dst []byte) {
		// unsafe 重解释：float32 切片 -> 原始字节，避免逐元素 binary 编码。
		copy(dst, unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*4))
	})
}

func float64Array(v []float64) js.Value {
	return typedArray("Float64Array", len(v), 8, func(dst []byte) {
		copy(dst, unsafe.Slice((*byte)(unsafe.Pointer(&v[0])), len(v)*8))
	})
}
