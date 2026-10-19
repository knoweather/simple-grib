# simple-grib

> [!NOTE]
> 🚀 **首次发版，灰度测试中。** 有任何问题欢迎提 [issue](https://github.com/knoweather/simple-grib/issues)！

**简体中文** | [English](./README.md)

纯 Go 的 **GRIB edition 1 + 2**（WMO 气象数据格式）读写库。零 C 依赖：`CGO_ENABLED=0 go build ./...` 干净通过。[simple-netcdf](https://github.com/knoweather/simple-netcdf) 的姊妹项目。

GRIB1 支持（2026-10-15 落地）：`grib.Open` 自动按版本分派——GRIB1 消息走 `grib1/` 包解析（IS/PDS/GDS/BMS/BDS 五段、IBM 浮点参考值、符号-幅度比例因子、bitmap 展开、简单打包位流），坐标复用 GRIB2 侧已验证的坐标生成器（规则经纬 / 高斯 / 旋转经纬 / 极射赤面 / 兰勃特 / 准规则）；GRIB1 与 GRIB2 可混装在同一文件。GRIB1 数值原生精度是 float64（`Message.Values64()`）。参数名要按 (中心, 表版本) 注册表 2（`grib1.RegisterTable2`）才有值。

- **Spec-as-data**：规范数据（429+ 张代码表、254 个模板描述符）直接生成自 WMO 官方机器可读 CSV（`specdata/`，含 provenance 戳），正确性由数据保证，不靠手抄常量
- **真实语料位级验证**：解码结果与 eccodes 在 CMA/ECMWF/GFS/JMA/ICON 等真实文件上逐点对拍（见[正确性](#正确性)）
- **fail-loud 契约**：未知模板、local-use 值、损坏/截断、非 GRIB（GRIB3/BUFR）→ 带上下文的错误，永不 panic、永不静默

## 特性

| 能力 | 状态 |
|---|---|
| 多消息文件索引 + section 0–7 结构解析与校验（GRIB2） | ✅ |
| **GRIB1 解码**：五段解析 + 数值（float64 原生精度）+ 坐标；与 GRIB2 可混装 | ✅ |
| 元数据：Identification / Product（含 WMO 码表名）、`Walk` 通用模板遍历器（254 模板一个解析器） | ✅ |
| 网格坐标：3.0/3.1/3.2/3.3（+rotated）、3.40/3.41 Gaussian、**3.20 polar stereographic / 3.30 Lambert conformal 逐点坐标** | ✅ |
| 数据解码：5.0 simple（任意位宽位流）、5.2 complex、5.3 complex+spatial differencing、5.4 IEEE binary32、**5.40 JPEG 2000（纯 Go JPC 解码器）**、5.42 CCSDS AEC（自研 codec） | ✅ |
| bitmap（section 6，indicator 0/255）+ 声明点数交叉校验 | ✅ |
| writer `gribw`：模板驱动编码 5.0 / 5.4，round-trip 测试；local 表注册 | ✅ |
| writer `gribw1`：GRIB1 编码（简单打包，类型 0/4 网格 + 位图 + 常量场） | ✅ |
| CLI `gribdump`：元数据 / 数值 / 逐字段 Walk / JSON | ✅ |
| **WASM** `wasm/`：浏览器 / Node 里直接解码（GRIB1 + GRIB2），元数据 + 数值 + 坐标以 TypedArray 出参；含拖拽即用的查看器 | ✅ |

## 安装

```bash
go get github.com/knoweather/simple-grib
```

## 快速开始

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test -count=1 ./...

go run ./cmd/gribdump sample.grib2 # 每条消息的元数据（带码表名）
go run ./cmd/gribdump -values 16 sample.grib2 # 前 16 个解码值
go run ./cmd/gribdump -section 3 -section 5 sample.grib2 # Walk 逐字段
go run ./cmd/gribdump -json sample.grib2 # 机器可读
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

**实测兼容性**：`GOOS=js GOARCH=wasm go build ./...` 11/11 包干净通过；`spec` / `grid` / `gribw` /
`internal/octetio` / `probe` / `cmd/gribdump` / `wasm` 的测试在 Node 下全绿。少数失败与库逻辑无关：
js/wasm 在 **Windows 宿主**下不支持读目录（Linux 宿主可以）、测试内部起 `go build` 子进程、
以及**跨架构位级浮点差异**（amd64 `GOAMD64=v3` 的 FMA 融合 + `math.Exp/Pow` 的 amd64 汇编实现，
与 wasm 差 1~4 ulp）—— 结论是位级 sha256 锚不能跨架构复用，跨架构校验要用容差。逐项证据见
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

1. **Spec-as-data**——WMO CSV 是唯一事实源；`cmd/specgen` 生成 `spec/tables_gen.go` + `templates_gen.go`（带 provenance 戳），`-check` 检测漂移；每次 WMO 修订 = 重同步 + 重生成，流程可审计。
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
- IEEE 5.4 仅 precision=1（binary32）；64/128-bit → fail-loud
- bitmap indicator 1–254（中心预定）→ fail-loud
- 网格：其余投影类（3.10/3.12/3.31…）与 stretched Gaussian（3.42/3.43）为 metadata-only，`Point/Lats/Lons` → `grid.ErrNotImplemented`
- writer 现支持 5.0 / 5.4(precision=1)（GRIB2）与 **`gribw1`：GRIB1 简单打包编码**（类型 0/4 网格、位图、常量场；round-trip 对拍真实文件通过）；BUFR 仅识别并报错

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
- **[腾讯 WorkBuddy](https://cloud.tencent.com/product/workbuddy)**
