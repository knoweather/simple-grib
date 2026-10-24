<p align="center">
  <img src="docs/logo.svg" width="128" alt="The Go gopher holding a small globe">
</p>

<h1 align="center">simple-grib</h1>

<p align="center">
<a href="https://github.com/knoweather/simple-grib/actions/workflows/ci.yml"><img src="https://github.com/knoweather/simple-grib/actions/workflows/ci.yml/badge.svg?branch=main" alt="CI"></a>
<a href="https://goreportcard.com/report/github.com/knoweather/simple-grib"><img src="https://goreportcard.com/badge/github.com/knoweather/simple-grib" alt="Go Report Card"></a>
<a href="https://scorecard.dev/viewer/?uri=github.com/knoweather/simple-grib"><img src="https://img.shields.io/ossf-scorecard/github.com/knoweather/simple-grib?label=openssf%20scorecard" alt="OpenSSF Scorecard"></a>
<a href="https://pkg.go.dev/github.com/knoweather/simple-grib"><img src="https://pkg.go.dev/badge/github.com/knoweather/simple-grib.svg" alt="Go Reference"></a>
<a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/knoweather/simple-grib" alt="Go version"></a>
<a href="LICENSE"><img src="https://img.shields.io/github/license/knoweather/simple-grib" alt="License"></a>
<a href="https://github.com/knoweather/simple-grib/tags"><img src="https://img.shields.io/github/v/tag/knoweather/simple-grib?sort=semver" alt="Latest tag"></a>
<img src="https://img.shields.io/badge/CGO-disabled-success" alt="CGO disabled">
<img src="https://img.shields.io/badge/WASM-browser%20%26%20node-blueviolet" alt="WASM">
</p>

<p align="center"><a href="./README.md">English</a> | <strong>简体中文</strong></p>

纯 Go 的 **GRIB edition 1 + 2** 读写库 —— 零 cgo、不需要 eccodes、不需要 C 工具链。
唯一非标准库依赖是 local-use WebP 模板背后的纯 Go [`simple-webp`](https://github.com/knoweather/simple-webp) 解码器。元数据逐键对齐 ecCodes，数值逐位一致，同一份代码还能编译成 WebAssembly。

```go
f, err := grib.Open("icon.grib2")
if err != nil {
    log.Fatal(err)
}
defer f.Close()

for m, err := range f.Messages() {
    if err != nil {
        log.Fatal(err)
    }
    p, _ := m.Product()
    q, _ := paramdb.QueryFrom(m)
    fmt.Println(p.ParameterName, paramdb.ShortNameOrUnknown(q), paramdb.UnitsOrUnknown(q))
}
// Temperature t K
```

- **与 ecCodes 一致的元数据**：`shortName` / `name` / `paramId` / `units` /
  `typeOfLevel` / `parameterName` 由 vendored ecCodes 定义生成的概念链回答（本地中心 →
  master → ecmf → legacy，平手取 master；表 4.2 与表 2 都按消息自己的表版本取值），
  不是手写映射表。
- **数值逐位一致**：CMA LARP 四锚点 442 消息（358M 值）+ data_snd 11 件 1109 消息
  （788.8M 值，含 CCSDS 156/156、simple packing 69/69）与 eccodes 的 SHA-256 相同；
  JPEG 2000 另与 Jasper 解码器交叉验证。
- **GRIB1 / GRIB2 同一套 API**：`grib.Open` 按 edition 自动分派，GRIB1 保留原生
  `float64` 精度（`Message.Values64()`），同一文件可混装。
- **WASM 可用**：`wasm/` 可构建 `grib.wasm` 与浏览器查看器，无需服务器、无需 C 工具链
  （`CGO_ENABLED=0 go build ./...` 通过）。

> [!NOTE]
> 🚀 **首次发版，灰度测试中。** 有任何问题欢迎提 [issue](https://github.com/knoweather/simple-grib/issues)！

[simple-netcdf](https://github.com/knoweather/simple-netcdf) 的姊妹项目。

## GRIB1

2026-10-15 落地。`grib.Open` 自动按版本分派——GRIB1 消息走 `grib1/` 包解析（IS/PDS/GDS/BMS/BDS 五段、IBM 浮点参考值、符号-幅度比例因子、bitmap 展开、简单打包位流），坐标复用 GRIB2 侧已验证的坐标生成器（规则经纬 / 高斯 / 旋转经纬 / 极射赤面 / 兰勃特 / 准规则）；GRIB1 与 GRIB2 可混装在同一文件。GRIB1 数值原生精度是 float64（`Message.Values64()`）。GRIB1 的 `ParameterName` 默认按内置的 `grib1/2.<centre>.<version>.table` 取值——选表规则与 ecCodes 的 `codetable_title` 一致（`table2Version < 128` 走 WMO 表、`>= 128` 用消息的中心、`centre != 98 && subCentre == 98` 时改用 subCentre；表里没有该码时退化成十进制码字符串），`grib1.RegisterTable2` 仍可覆盖它。GRIB2 的 `ParameterName` 同理取自表 4.2 题名（`spec/paramname_gen.go`，按消息的表版本选择并钳制到最新版本）。

## 特性

| 能力 | 状态 |
|---|---|
| 多消息文件索引 + section 0–7 结构解析与校验（GRIB2） | ✅ |
| **GRIB1 解码**：五段解析 + 数值（float64 原生精度）+ 坐标；与 GRIB2 可混装 | ✅ |
| 元数据：Identification / Product（含 WMO 码表名）、`Walk` 通用模板遍历器（254 模板一个解析器） | ✅ |
| 网格坐标：3.0/3.1/3.2/3.3（+rotated）、3.40/3.41 Gaussian、**3.20 polar stereographic / 3.30 Lambert conformal 逐点坐标** | ✅ |
| 数据解码：5.0 simple（任意位宽位流）、5.2 complex、5.3 complex+spatial differencing、5.4 IEEE binary32、**5.40 JPEG 2000（纯 Go JPC 解码器）**、5.42 CCSDS AEC（自研 codec）；5.49152 承载 WebP 图像载荷（local-use，原样存储；头解析/解码经 `simple-webp`） | ✅ |
| bitmap（section 6，indicator 0/255）+ 声明点数交叉校验 | ✅ |
| writer `gribw`：模板驱动编码 5.0 / 5.4，round-trip 测试；5.49152 经 `SetWebP` 写入 WebP 图像；local 表注册 | ✅ |
| writer `gribw1`：GRIB1 编码（简单打包，类型 0/4 网格 + 位图 + 常量场） | ✅ |
| CLI `gribdump`：元数据 / 数值 / 逐字段 Walk / JSON / WebP 图像导出 | ✅ |
| **WASM** `wasm/`：浏览器 / Node 里直接解码（GRIB1 + GRIB2），元数据 + 数值 + 坐标以 TypedArray 出参；含拖拽即用的查看器 | ✅ |

## 安装

```bash
go get github.com/knoweather/simple-grib
```

每个 [release](https://github.com/knoweather/simple-grib/releases) 附带
linux/amd64、linux/arm64、linux/loong64、windows/amd64、darwin/arm64 的
`gribdump` 预编译二进制。

## 快速开始

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test -count=1 ./...

go run ./cmd/gribdump sample.grib2 # 每条消息的元数据（带码表名）
go run ./cmd/gribdump -values 16 sample.grib2 # 前 16 个解码值
go run ./cmd/gribdump -section 3 -section 5 sample.grib2 # Walk 逐字段
go run ./cmd/gribdump -json sample.grib2 # 机器可读
go run ./cmd/gribdump -webp satellite.webp sample.grib2 # 导出 WebP 图像载荷
```

## 示例

### 读：元数据 + 数值 + 坐标

```go
package main

import (
	"fmt"

	grib "github.com/knoweather/simple-grib"
)

func main() {
	fl, err := grib.Open("sample.grib2")
	if err != nil { panic(err) }
	defer fl.Close()

	n, _ := fl.NumMessages()
	for i := 0; i < n; i++ {
		m, err := fl.Message(i)
		if err != nil { panic(err) }
		p, err := m.Product()
		if err != nil { panic(err) }

		level := "n/a"
		if p.Level1 != nil {
			level = fmt.Sprintf("type=%d", p.Level1.Type)
			if p.Level1.HasValue {
				level = fmt.Sprintf("type=%d value=%g", p.Level1.Type, p.Level1.Scale)
			}
		}
		forecast := "n/a"
		if p.ForecastTime != nil && !p.ForecastTime.Overflow {
			forecast = p.ForecastTime.Value.String()
		}
		fmt.Printf("msg%d: %s level=%s forecast=%s\n",
			i, p.ParameterName, level, forecast)

		vals, err := m.Values() // 数据值（5.0/5.2/5.3/5.4/5.40/5.42）
		if err != nil { fmt.Println(" values:", err); continue }
		fmt.Printf(" %d values, first=%.4g\n", len(vals), vals[0])

		g, _ := m.Grid()
		if lat, lon, err := g.Point(0); err == nil {
			fmt.Printf(" point(0) = %.4f, %.4f\n", lat, lon)
		}
	}
}
```

### 写：simple packing round-trip

```go
import (
	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/gribw"
)

fl, _ := grib.Open("sample.grib2")
m, _ := fl.Message(0)
g, _ := m.Grid()
vals, _ := m.Values()

msg := gribw.New()
_ = msg.SetDiscipline(0)
_ = msg.SetIdentification(id) // *grib.Identification
_ = msg.SetGrid(g) // 可复用 reader 的网格定义
_ = msg.SetProduct(prod) // *grib.Product

sec5, payload, _ := gribw.EncodeSimple(vals, gribw.SimpleParams{R: 0, E: 4, D: 1, Bits: 10})
_ = msg.SetDataSection(sec5, payload)
data, _ := gribw.File(msg) // 完整 GRIB2 字节流（可变参支持多消息）
```

### WebP 图像载荷（local use）

GRIB 也能作为图像产品的容器：模板 `5.49152` 原样保存一整份 WebP 文件——库既不在写入时解析像素，也不把数值重编码成图像。这类消息的 `Values()` 返回 `pack.ErrNotNumeric`，请改用图像访问器。

```go
webp, _ := os.ReadFile("satellite.webp")

msg := gribw.New()
_ = msg.SetDiscipline(0)
_ = msg.SetIdentification(id)
_ = msg.SetGrid(g)
_ = msg.SetProduct(prod)
_ = msg.SetWebP(webp)   // 第 5 段 = 11 字节固定头，第 7 段 = 文件原字节
data, _ := gribw.File(msg)

m, _ := fl.Message(0)   // reader 侧
raw, _ := m.WebP()      // 原样取回
info, _ := m.WebPInfo() // {Width, Height, Lossless, Animated}，只读头
img, _ := m.WebPImage() // 解码后的 image.Image（动画取首帧）
```

## WASM（浏览器 / Node）

纯 Go + 零 CGO，因此**直接就能编成 WebAssembly**：前端拿字节流解码 GRIB2，数据不出浏览器，不需要后端。
入口在 [`wasm/`](wasm/README.md)。

```sh
GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -o wasm/grib.wasm ./wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" wasm/ # 胶水必须与 Go 版本配套
cd wasm && python3 -m http.server 8000 # 浏览器开 http://localhost:8000/viewer.html
```

想省掉起服务器这一步，就打个单文件版（内联胶水 + gzip 后的 wasm ≈ 1.9 MB，双击即用）：

```sh
go run ./wasm/tools/mkstandalone -html wasm/viewer.html -wasm wasm/grib.wasm \
 -glue "$(go env GOROOT)/lib/wasm/wasm_exec.js" -out wasm/viewer.standalone.html
```

```js
const res = simpleGrib.decode(new Uint8Array(await file.arrayBuffer()),
 { values: true, points: true, perPoint: true });
res.messages[0].product.parameterName; // "Temperature"
res.messages[0].values; // Float32Array
res.messages[0].grid.lats; // Float64Array（perPoint 时与 values 同序）
```

`wasm/viewer.html` 是个现成的单文件查看器：**拖入 `.grib2` 就出结果** —— 消息列表、identification/product/grid 元数据（码表名由 Go 侧查好）、数值场渲染（规则网格画栅格、投影网格自动改画散点，含悬停读数）、前 200 行数值表，还能导出 CSV / PNG / 元数据 JSON。

**实测兼容性**：`GOOS=js GOARCH=wasm go build ./...` 所有包干净通过；CI 在 Linux 上用 Node 跑完整测试。
坐标锚按平台分流：packed-float64 digest 只在已记录的 amd64 / darwin-arm64 构建上逐位断言，其它平台
（wasm、其它 GOARCH/GOAMD64）改验 192 个采样点 ≤1e-9°。宿主专属检查（起 `go build` 子进程、fuzz）
在 js/wasm 下自动 skip；**Windows 宿主**的 js/wasm 仍不支持读目录（Linux 宿主可以）。逐项证据见
[`wasm/README.md`](wasm/README.md#兼容性实测)。

## 性能对比

AMD 4C 主机实跑（Debian 13，go1.24.4 / 系统 eccodes 2.41.0），语料为 47 件真实数据（CMA LARP TRAMS/GRAPES、ECMWF ICON/agl/atmosphere、GFS pgrb2、JMA GPV、CCSDS、pangu 等 10 类）。协议：每引擎 1 次预热 + 10 轮计时取均值；单轮 = 打开文件 + 全部消息 `Values()`。驱动与口径见 [`bench/p7_decode_bench/`](bench/p7_decode_bench/README.md)。

**双方均可解码的 42 件（350.13 MB / 485,311,281 值 / 1030 消息）：**

| 类别 | 件数 | 体积 | simple-grib (Go) | eccodes (C) | 说明 |
|---|---:|---:|---:|---:|---|
| **JPEG 2000（5.40）** CMA LARP | 4 | 153.3 MB | 3.5 MB/s | **9.3 MB/s** | 纯 Go JPC 解码器 vs Jasper C，当前主要差距 |
| **simple packing 大文件** ICON/pangu | 5 | 139.1 MB | **681 MB/s** | 678 MB/s | 持平（≈1.00×） |
| GFS pgrb2 多字段 | 3 | 34.4 MB | **453 MB/s** | 229 MB/s | Go 快 ≈2.0× |
| JMA GPV 多字段 | 9 | 19.5 MB | 246 MB/s | **428 MB/s** | eccodes 快 ≈1.7×（含 multifield-slice 口径差） |
| ECMWF 小文件（agl/atmosphere/CCSDS…） | 21 | 3.8 MB | **326 MB/s** | 74 MB/s | Go 快 ≈4.4×（每消息开销路径） |
| **合计** | 42 | 350.1 MB | 8.0 MB/s · 11.1M val/s | **20.7 MB/s · 28.7M val/s** | 总吞吐 eccodes ≈2.6×（被 JPC 件拉低） |

混合语料 fail-loud 实证（GRIB1 支持落地前）：5 件非 GRIB2 全部带上下文拒绝——GRIB edition 1 ×2、BUFR ×2（magic `BUFR`）、0 字节截断 ×1；失败件不静默剔除，随结果报告。（GRIB1 现已支持，edition 3 / BUFR 仍按此拒绝。）

复跑（仓库根目录）：

```sh
CGO_ENABLED=0 go run ./bench/p7_decode_bench --rounds 10 <files...> > go_results.jsonl
python3 -u bench/p7_decode_bench/eccodes_bench.py --rounds 10 <files...> > py_results.jsonl
```

口径备注：simple-grib 列为纯数据值路径（坐标惰性生成不计入）；eccodes 经 Python 绑定逐消息调用 C API（封装开销在计时路径内）；JMA GPV 多字段封套在 eccodes 侧按预建 per-field slice 解码（该绑定构建无 next-message API）。

## 设计

设计要点（Spec-as-data / 解析解释分离 / 三层分离 / 优雅降级 / 写入对称，详见要点描述）：

1. **Spec-as-data**——WMO CSV 加上 vendored 的 ecCodes definitions（见 `paramdata/PROVENANCE.txt`）是唯一事实源；`cmd/specgen` 生成 `spec/tables_gen.go` + `templates_gen.go` + `paramname_gen.go`（带版本的表 4.2 标题池），`cmd/paramgen` 生成概念 / MARS / GRIB1 表 2 数据（`paramdb/concepts_gen.go`、`paramdb/mars_gen.go`、`grib1/mars_gen.go`、`grib1/table2_gen.go`、`mars_gen.go`），两者都有 `-check` 漂移检测；每次 WMO/ecCodes 修订 = 重同步 + 重生成，流程可审计。
2. **解析与解释分离**——一个通用模板引擎（`Walk`）按生成的描述符解析全部 254 个模板；语义解释（网格坐标、打包解码）只在需要的地方按模板号实现，未实现的暴露原始字段而不是失败。
3. **三层分离**——L1 线格式（节/字节/位）→ L2 结构（模板/代码表，纯数据）→ L3 语义（grid/pack/Product），每层可独立测试。
4. **优雅降级是一等公民**——未知模板号、local-use 值 → typed 访问器返回带上下文的错误，raw 字段永远可达，永不 panic。
5. **写入对称**——同一份 `TemplateDef` 描述符驱动读和写（`gribw`），避免两套结构定义漂移。

```
L3 语义层 grid/（网格→坐标） pack/（数据表示→数值，注册表+内置解码器） (root) Product/Identification
L2 结构层 spec：WMO CSV ──specgen──► Go 表/模板描述符；Table.Lookup / Template / Walk
L1 线格式层 File（io.ReaderAt + 消息索引）→ Message（整条缓冲）→ Section（零拷贝切片）；大端读取 / BitReader
```

## 正确性

- **真实语料位级对拍**：CMA LARP 四锚点 442 消息（358M 值）+ data_snd 11 件 1109 消息（788.8M 值，含 CCSDS 156/156、simple pack 69/69）与 eccodes 逐点 SHA-256 一致；JPEG 2000 另经 Jasper 自身解码器交叉验证。
- **eccodes 二进制对拍向量**（`testdata/ecc-xcheck/`）：simple packing 576/576、IEEE 108/108（含 ±Inf/NaN/次正规数）、regular/Gaussian/旋转网格逐点 max diff = 0 / 4.26e-14° / 9.9e-7°。
- **CCSDS 5.42 oracle**：36/36 PASS（`scripts/xcheck/ccsds-oracle.sh`，digest 锚在脚本头注）。

## 已知限制

- 数据模板 5.1 / 5.41 PNG / 5.5x spectral / 5.200 run-length → `pack.ErrUnsupported` fail-loud
- JPEG 2000 5.40：仅支持可逆 5/3 变换、LRCP、单层、单分量；不可逆 9/7、多层/多分量码流，以及 COC/QCC/RGN/POC/PPM/PPT/SOP/EPH marker 段 → `pack.ErrJPCUnsupported`
- local-use 模板 49152（WebP）是**不透明的图像载荷**：`SetWebP`/`WebP` 原字节存取，`Values` 返回 `pack.ErrNotNumeric`；有损、动画都接受（动画由 `WebPImage` 取首帧）；图像尺寸不必等于网格点数——`numberOfValues` 记录的是像素数
- IEEE 5.4 仅 precision=1（binary32）；64/128-bit → fail-loud
- bitmap indicator 1–254（中心预定）→ fail-loud
- 网格：其余投影类（3.10/3.12/3.31…）与 stretched Gaussian（3.42/3.43）为 metadata-only，`Point/Lats/Lons` → `grid.ErrNotImplemented`
- writer 现支持 5.0 / 5.4(precision=1)（GRIB2）、local-use 5.49152 WebP 图像载荷（`SetWebP`）与 **`gribw1`：GRIB1 简单打包编码**（类型 0/4 网格、位图、常量场；round-trip 对拍真实文件通过）；BUFR 仅识别并报错

## 文档索引

| 文档 | 内容 |
|---|---|
| [wasm/README.md](wasm/README.md) | WASM 入口：构建、JS API、浏览器查看器、跨架构兼容性实测 |
| [bench/p7_decode_bench/README.md](bench/p7_decode_bench/README.md) | 跑分驱动与协议说明 |

## AI 加成

模型致谢：

- **Powered by [GLM-5.3 Flash](https://z.ai)** — 特别感谢
 [OpenCode](https://opencode.ai) 提供的免费额度，促成了本次开发
- **[Qwen 3.8 Flash](https://github.com/QwenLM)** — 开源模型，本地部署运行
- **[WorkBuddy](https://www.workbuddy.cn)**

## 致谢

Go gopher 由 [Renee French](https://reneefrench.blogspot.com/) 设计，采用
[CC BY 3.0](https://creativecommons.org/licenses/by/3.0/) 许可；
[`docs/logo.svg`](docs/logo.svg) 的 logo 是基于该形象的演绎作品。
