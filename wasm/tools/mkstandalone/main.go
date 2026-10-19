// mkstandalone 把 wasm/viewer.html 打成"双击就能用"的单文件版本：
// 内联 wasm_exec.js，并把 grib.wasm 用 gzip + base64 塞进页面。
//
// 用法（在仓库根目录）：
//
//	go run ./wasm/tools/mkstandalone \
//	  -html wasm/viewer.html -wasm wasm/grib.wasm \
//	  -glue "$(go env GOROOT)/lib/wasm/wasm_exec.js" \
//	  -out wasm/viewer.standalone.html
//
// 为什么要 gzip：wasm 里绝大部分是 WMO 码表字符串，压完只剩三分之一左右；
// 浏览器侧用 DecompressionStream('gzip') 解回来，不需要任何第三方库。
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
)

const (
	glueTag    = `<script src="wasm_exec.js"></script>`
	payloadTag = `/* WBGRIB:PAYLOAD */`
)

func main() {
	html := flag.String("html", "wasm/viewer.html", "viewer 模板")
	wasm := flag.String("wasm", "wasm/grib.wasm", "编译好的 wasm 模块")
	glue := flag.String("glue", "", "wasm_exec.js 路径（必填，通常在 $(go env GOROOT)/lib/wasm/）")
	out := flag.String("out", "wasm/viewer.standalone.html", "输出文件")
	flag.Parse()

	if *glue == "" {
		fatal("缺少 -glue：它是 Go 版本相关的胶水，必须显式给出（$(go env GOROOT)/lib/wasm/wasm_exec.js）")
	}

	tpl, err := os.ReadFile(*html)
	if err != nil {
		fatal("读模板: %v", err)
	}
	glueJS, err := os.ReadFile(*glue)
	if err != nil {
		fatal("读胶水: %v", err)
	}
	wasmBin, err := os.ReadFile(*wasm)
	if err != nil {
		fatal("读 wasm: %v", err)
	}

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		fatal("gzip: %v", err)
	}
	if _, err := zw.Write(wasmBin); err != nil {
		fatal("gzip 写: %v", err)
	}
	if err := zw.Close(); err != nil {
		fatal("gzip 关: %v", err)
	}
	payload := base64.StdEncoding.EncodeToString(buf.Bytes())

	// 胶水里出现 </script> 会提前结束脚本块（Go 的 wasm_exec.js 目前没有，
	// 但以后加了也会被这里挡掉）。
	safeGlue := strings.ReplaceAll(string(glueJS), "</script", `<\/script`)

	s := string(tpl)
	if !strings.Contains(s, glueTag) {
		fatal("模板里找不到 %s", glueTag)
	}
	if !strings.Contains(s, payloadTag) {
		fatal("模板里找不到 %s", payloadTag)
	}
	s = strings.Replace(s, glueTag, "<script>\n"+safeGlue+"\n</script>", 1)
	s = strings.Replace(s, payloadTag, `window.__WBGRIB_WASM_GZ__="`+payload+`";`, 1)

	if err := os.WriteFile(*out, []byte(s), 0o644); err != nil {
		fatal("写输出: %v", err)
	}

	fmt.Printf("wasm %d B → gzip %d B → base64 %d B\n", len(wasmBin), buf.Len(), len(payload))
	fmt.Printf("写出 %s（%.1f MB，双击即可打开，无需服务器）\n", *out, float64(len(s))/1024/1024)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mkstandalone: "+format+"\n", args...)
	os.Exit(1)
}
