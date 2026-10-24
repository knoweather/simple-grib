# wasm — 在浏览器 / Node 里跑 simple-grib

把库编成 WebAssembly，前端直接解码 GRIB1/GRIB2：**数据不出浏览器**，没有后端、没有 C 依赖。

## 构建

```sh
# 1) 编译 wasm 模块
GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -o wasm/grib.wasm ./wasm

# 2) 拷贝与当前 Go 版本配套的胶水（版本必须一致，别用别处的）
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" wasm/

# 3) 起个本地服务器打开查看器（file:// 下 fetch 拿不到 .wasm）
cd wasm && python3 -m http.server 8000 # 然后浏览器开 http://localhost:8000/viewer.html
```

想省掉"起服务器"这一步，就打一个单文件版（内联胶水 + gzip 后的 wasm，约 1.9 MB，双击即用）：

```sh
go run ./wasm/tools/mkstandalone \
 -html wasm/viewer.html -wasm wasm/grib.wasm \
 -glue "$(go env GOROOT)/lib/wasm/wasm_exec.js" \
 -out wasm/viewer.standalone.html
```

> Go 1.27 的胶水是 `lib/wasm/wasm_exec.js`（老版本叫 `misc/wasm/wasm_exec.js` / `wasm_exec.html`）。
> 仓库不提交 `grib.wasm` / `wasm_exec.js` / `viewer.standalone.html`（构建产物，且胶水与工具链版本强绑定），
> 已在 `.gitignore` 里排除。

## JS API

模块加载后（`go.run(instance)`）会在 `globalThis` 上挂一个 `simpleGrib`：

```js
const bytes = new Uint8Array(await file.arrayBuffer());

// 全量：元数据 + 数值 + 坐标
const res = simpleGrib.decode(bytes, { values: true, points: true, perPoint: true });

// 只要元数据（不碰 section 7，大文件秒回）
const md = simpleGrib.metadata(bytes);

simpleGrib.version(); // "0.1.0"
```

| 入参 | 类型 | 说明 |
|---|---|---|
| `bytes` | `Uint8Array` \| `ArrayBuffer` \| 任意 TypedArray \| `number[]` | 一段或多段拼接的 GRIB 字节流（GRIB1 与 GRIB2 均可） |
| `opts.values` | `bool`（默认 `decode` 为 true、`metadata` 为 false） | 是否解码数值 |
| `opts.points` | `bool`（默认 true） | 是否生成坐标 |
| `opts.perPoint` | `bool`（默认 false） | 规则网格是否把"行纬度/列经度"展开成与 `values` 逐点对齐的完整数组 |

返回对象：

```
{
 ok: bool, bytes, numMessages,
 messages: [{
 index, offset, size,
 edition, // 1 或 2：段编号/码空间随版本不同，先看它
 discipline, disciplineName, // 表 0.0 的名字（仅 GRIB2；GRIB1 无此概念）
 sections: [0,1,3,4,5,6,7],
 sectionSizes: [298426, 21, ...], // 与 sections 一一对应
 identification: { centre, centreName, subCentre, masterTableVersion, localTableVersion,
 referenceTime, // RFC3339 UTC
 productionStatus, productionStatusName, // 表 1.3
 typeOfProcessedData, typeOfProcessedDataName, // 表 1.4
 calendarTemplate }, // -1 = section 1 未带日历定义
 product: { templateNumber, templateName,
 parameterCategory, parameterNumber, parameterName,
 typeOfGeneratingProcess, nv,
 forecastTime: { unit, raw, seconds, overflow },
 level1: { type, typeName, scale, hasValue }, level2: {...} }, // typeName = 表 4.5
 grid: { templateNumber, templateName, ni, nj, numPoints, quasiRegular,
 shapeOfEarth, radius,
 coordinateMode: "rowsCols" | "perPoint" | "",
 pointsError?, lats: Float64Array, lons: Float64Array },
 dataRep: { templateNumber, templateName, numValues },
 values: Float32Array | Float64Array,
 // 仅 opts.values=true。GRIB2 是 Float32Array（binary32 原生）；
 // GRIB1 是 Float64Array（取值公式在 float64 下算，原生精度）
 stats: { count, min, max, mean, nanCount },
 errors: [ "...", ... ] // 该条消息内部的失败原因（不连坐其它消息）
 }],
 errors: [ ... ] // 文件级失败（连索引都建不起来时）
}
```

码表名（`disciplineName` / `centreName` / `productionStatusName` / `typeName` / `*TemplateName`）
都由 Go 侧查好带出来 —— 前端不必再抄一遍 WMO 表。GRIB1 的层类型名 / 网格类型名 /
时间单位名来自 `grib1` 包的表 3 / 表 6 / 表 4（GRIB2 的对应码查表 4.5 / 3.x / 4.4）；
GRIB1 参数名要按 (中心, 表版本) 注册表 2（`grib1.RegisterTable2`）才有值，未注册时为空串。

GRIB1 消息的 `identification.productionStatus` / `typeOfProcessedData` 为 -1（该版本无此概念）；
`product.templateNumber` / `dataRep.templateNumber` 也为 -1。

### 坐标的两种语义

- `coordinateMode: "rowsCols"` —— 规则 / Gaussian 网格：`grid.lats` 是 **Nj 个行纬度**，
 `grid.lons` 是 **Ni 个列经度**（可分离，省 O(点数) 内存）。第 j 行第 i 列 = `(lats[j], lons[i])`。
- `coordinateMode: "perPoint"` —— 投影网格（3.20/3.30）本来就逐点；或你显式传了
 `perPoint: true` 把规则网格展开。此时 `lats[i]` / `lons[i]` 与 `values[i]` **严格同序**，
 扫描方向（WE:SN、NS、交替行等）已由库处理掉，前端可以无脑逐点配对。

## 浏览器查看器

`viewer.html` 是个单文件查看器，拖入 `.grib2` 就出结果：

- **拖拽即解析**：整个窗口都是放置区；数据不出本机（浏览器里直接解码）
- **消息列表**：多消息文件逐条列出（参数名、中心、网格尺寸、值数、层级），`←/→` 切换
- **场渲染**：规则经纬 / Gaussian 网格画成栅格（自动处理蛇形扫描与南北朝向），
 投影网格（3.20/3.30）自动改画散点；4 种色阶 + 自动量程 + 手动 min/max + 悬停读数（值 / lat / lon / 索引）
- **元数据**：identification / product / grid / data representation / 每个 section 的字节数，
 码表名（0.0 / 1.3 / 1.4 / 4.5、模板名）都在 Go 侧查好带出来，前端不抄 WMO 表
- **数值**：统计摘要 + 前 200 行（含逐点经纬度）表格；导出全部值 CSV / 导出 PNG / 复制元数据 JSON
- **fail-loud 可视化**：每条消息自己的错误单独展示，不连坐其它消息

`window.__viewerReady` 在每次解析结束后置 true，方便写自动化冒烟测试。
`?sample=N` 会按 `SAMPLES` 表里第 N 个样本自动载入（从仓库根目录起服务器时可用）。

## wasm 兼容性说明

在 `GOOS=js GOARCH=wasm` 下 `go build ./...` 全部包均通过编译；CI 在 Linux 上用
Node 跑完整测试（`go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./...`）。

| 包 | Linux 宿主（CI） | Windows 宿主（本地实测） |
|---|---|---|
| `spec` `grid` `gribw` `internal/octetio` `probe` `cmd/gribdump` `wasm` | 全部通过 | 全部通过 |
| 根包 | 全部通过 | 2 个读目录用例失败（见下），其余通过 |
| `pack` | 全部通过 | 1 个读目录用例失败 |
| `bench/p7_decode_bench` `cmd/specgen` | — 无测试文件 | — |

三处与库逻辑无关的环境/平台差异：

1. **读目录**：Go 的 js/wasm `syscall.Open` 在 **Windows 宿主**上不支持 `O_DIRECTORY`
  （`syscall/fs_js.go`：Node 的 libuv 在 Windows 上没这个常量）；Linux 宿主下正常。
  受影响：`pack/xcheck_test.go`、`xcheck_p8_a_differential_test.go`、
  `xcheck_p8_a_parallel_test.go`。
2. **宿主专属检查自动 skip**：`xcheck_p6_a_cli_test.go`（起 `go build` 子进程）与
  `FuzzOpen`（fuzz 需要宿主覆盖插桩）在 js/wasm 下 `t.Skip`，不计为失败。
3. **跨架构浮点**：坐标锚按平台分流——已记录的 amd64 / darwin-arm64 构建继续做
  packed-float64 sha256 逐位断言；wasm 等其它平台改验 192 个采样点
  （`grid_anchor_test.go`，≤1e-9°）。wasm 与 amd64 的实际差仍是 **1~4 ulp**
  （≈1e-14 度），采样容差有六个数量级余量，真正的公式/扫描序回归仍会失败。
  根因两条：`GOAMD64=v3` 的 FMA 融合，以及 `math.Exp`/`math.Pow` 的 amd64
  汇编实现与 wasm 通用实现不同。

### 怎么在 Node 里跑 wasm 测试

```sh
export GOOS=js GOARCH=wasm CGO_ENABLED=0

# Linux/macOS（CI 同款）：工具链自带的 bash 包装器
go test -count=1 -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./...

# Windows：go_js_wasm_exec 是 bash 脚本，改用 node 直接跑胶水
go test -c -o /tmp/x.test.wasm ./grid
node "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" /tmp/x.test.wasm -test.v
```

> 注意两点：① 宿主的全部环境变量会被塞进 wasm 实例，Windows 上很容易撞
> `Error: total length of command line and environment variables exceeds limit`，
> 用 `env -i PATH=... ` 起个干净环境即可；② 测试用例里读 `testdata/` 用的是相对路径，
> 所以要 `cd` 到包目录再跑（`go test` 平时就是这么做的）。

## 已知限制

- **不支持的字节参数**：`bytes` 必须是 TypedArray / ArrayBuffer / `number[]`；传字符串或数字会返回
 `{ok:false, errors:[...]}` 而不是抛异常。
- **内存**：整段文件与解码结果都驻留在 wasm 线性内存里（默认上限 4 GiB，但浏览器 tab 实际可用更少）。
 超大文件建议先 `metadata()` 看规模，再决定要不要 `values`。
- **坐标生成是 O(点数)**：全球 0.25° 场（约 100 万点）`perPoint` 会产生 16 MB 的
 `Float64Array`。只看元数据或只用 `rowsCols` 就能避开。
- **线程**：wasm 下 `GOMAXPROCS=1`，库内部的并行解码退化为串行（结果不变，只是不加速）。
- **不支持的能力与原生构建一致**：5.1 / 5.41 PNG / 5.5x spectral / 5.200 等仍是
 fail-loud（见根 README「已知限制」）。
