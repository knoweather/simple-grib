# simple-grib

> [!NOTE]
> 🚀 **Initial release, canary testing in progress.** If you hit any problem, please open an [issue](https://github.com/knoweather/simple-grib/issues)!

**English** | [简体中文](./README.zh-CN.md)

A pure-Go library to read and write **GRIB edition 1 + 2** (the WMO meteorological
data format). Zero C dependencies: `CGO_ENABLED=0 go build ./...` passes clean.
A sibling project of [simple-netcdf](https://github.com/knoweather/simple-netcdf).

GRIB1 support (landed 2026-10-15): `grib.Open` dispatches automatically by
edition — GRIB1 messages are parsed by the `grib1/` package (five sections
IS/PDS/GDS/BMS/BDS, IBM-float reference value, sign-magnitude scale factors,
bitmap expansion, simple-packed bit stream), reusing the grid-coordinate
generators already verified on the GRIB2 side (regular lat/lon, Gaussian,
rotated lat/lon, polar stereographic, Lambert conformal, quasi-regular);
GRIB1 and GRIB2 messages may be mixed within one file. GRIB1 values keep
native float64 precision (`Message.Values64()`); parameter names resolve once
a table 2 is registered per (centre, table version) via `grib1.RegisterTable2`.

- **Spec-as-data**: the spec data (429+ code tables, 254 template descriptors)
 is generated directly from the official WMO machine-readable CSVs
 (`specdata/`, provenance-stamped) — correctness comes from the data, not
 from hand-copied constants
- **Bit-level verification on real corpora**: decoded values are compared
 point by point with eccodes on real CMA/ECMWF/GFS/JMA/ICON files
 (see [Correctness](#correctness))
- **fail-loud contract**: unknown templates, local-use values, damaged or
 truncated files, non-GRIB (GRIB3/BUFR) → contextual errors; never panics,
 never stays silent

## Features

| Capability | Status |
|---|---|
| Multi-message file index + section 0–7 structural parsing and validation (GRIB2) | ✅ |
| **GRIB1 decoding**: five-section parse + values (native float64) + coordinates; mixable with GRIB2 | ✅ |
| Metadata: Identification / Product (with WMO code-table names), `Walk` generic template walker (one parser for all 254 templates) | ✅ |
| Grid coordinates: 3.0/3.1/3.2/3.3 (+rotated), 3.40/3.41 Gaussian, **3.20 polar stereographic / 3.30 Lambert conformal point-by-point** | ✅ |
| Data decoding: 5.0 simple (any bit width), 5.2 complex, 5.3 complex+spatial differencing, 5.4 IEEE binary32, **5.40 JPEG 2000 (pure-Go JPC decoder)**, 5.42 CCSDS AEC (own codec) | ✅ |
| Bitmap (section 6, indicator 0/255) + declared point count cross-check | ✅ |
| Writers: `gribw` templates 5.0 / 5.4 with round-trip tests; local table registration | ✅ |
| Writer `gribw1`: GRIB1 encoding (simple packing, grid types 0/4 + bitmap + constant fields) | ✅ |
| CLI `gribdump`: metadata / values / field-by-field `Walk` / JSON | ✅ |
| **WASM** `wasm/`: decode GRIB1 + GRIB2 directly in the browser / Node; metadata + values + coordinates as TypedArrays, drag-and-drop viewer included | ✅ |

## Install

```bash
go get github.com/knoweather/simple-grib
```

## Quick start

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test -count=1 ./...

go run ./cmd/gribdump sample.grib2 # per-message metadata (with code-table names)
go run ./cmd/gribdump -values 16 sample.grib2 # first 16 decoded values
go run ./cmd/gribdump -section 3 -section 5 sample.grib2 # field-by-field Walk
go run ./cmd/gribdump -json sample.grib2 # machine readable
```

## Examples

### Reading: metadata + values + coordinates

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

		vals, err := m.Values() // data values (5.0/5.2/5.3/5.4/5.40/5.42)
		if err != nil { fmt.Println(" values:", err); continue }
		fmt.Printf(" %d values, first=%.4g\n", len(vals), vals[0])

		g, _ := m.Grid()
		if lat, lon, err := g.Point(0); err == nil {
			fmt.Printf(" point(0) = %.4f, %.4f\n", lat, lon)
		}
	}
}
```

### Writing: simple packing round trip

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
_ = msg.SetGrid(g) // reuse the reader's grid definition
_ = msg.SetProduct(prod) // *grib.Product

sec5, payload, _ := gribw.EncodeSimple(vals, gribw.SimpleParams{R: 0, E: 4, D: 1, Bits: 10})
_ = msg.SetDataSection(sec5, payload)
data, _ := gribw.File(msg) // complete GRIB2 byte stream (variadic: multiple messages)
```

## WASM (browser / Node)

Pure Go + zero CGO means it **builds to WebAssembly as-is**: decode GRIB2 from
a byte stream in the frontend — data never leaves the browser, no backend
needed. Entry point in [`wasm/`](wasm/README.md).

```sh
GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -o wasm/grib.wasm ./wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" wasm/ # glue must match the Go version
cd wasm && python3 -m http.server 8000 # open http://localhost:8000/viewer.html
```

To skip the server step, build a single-file bundle (inlined glue + gzipped
wasm ≈ 1.9 MB, just double-click it):

```sh
go run ./wasm/tools/mkstandalone -html wasm/viewer.html -wasm wasm/grib.wasm \
 -glue "$(go env GOROOT)/lib/wasm/wasm_exec.js" -out wasm/viewer.standalone.html
```

```js
const res = simpleGrib.decode(new Uint8Array(await file.arrayBuffer()),
 { values: true, points: true, perPoint: true });
res.messages[0].product.parameterName; // "Temperature"
res.messages[0].values; // Float32Array
res.messages[0].grid.lats; // Float64Array (same order as values with perPoint)
```

`wasm/viewer.html` is a ready-made single-file viewer: **drop a `.grib2` and
see results** — message list, identification/product/grid metadata
(code-table names resolved on the Go side), value-field rendering (regular
grids draw a raster, projected grids draw a scatter with hover readouts),
first 200 rows as a value table, plus CSV / PNG / metadata JSON export.

**Measured compatibility**: `GOOS=js GOARCH=wasm go build ./...` passes clean
on 11/11 packages; tests of `spec` / `grid` / `gribw` / `internal/octetio` /
`probe` / `cmd/gribdump` / `wasm` all pass under Node. The few failures are
unrelated to library logic: js/wasm under a **Windows host** cannot read
directories (Linux hosts can), tests that spawn `go build` subprocesses, and
**cross-architecture bit-level float differences** (amd64 `GOAMD64=v3` FMA
fusion + `math.Exp/Pow` assembly vs wasm, 1–4 ulp apart) — bit-level sha256
anchors therefore cannot be reused across architectures; cross-architecture
checks must use tolerances. Item-by-item evidence in
[`wasm/README.md`](wasm/README.md#兼容性实测).

## Performance comparison

Measured on an AMD 4C Debian 13 box (go1.24.4 vs system eccodes 2.41.0),
corpus = 47 real files from 10 families (CMA LARP TRAMS/GRAPES, ECMWF
ICON/agl/atmosphere, GFS pgrb2, JMA GPV, CCSDS, pangu, …). Protocol: one warmup
pass + 10 timed rounds averaged per engine; one round = open the file + call
`Values()` on every message. Driver and protocol in
[`bench/p7_decode_bench/`](bench/p7_decode_bench/README.md).

**The 42 files both engines can decode (350.13 MB / 485,311,281 values /
1030 messages):**

| Family | Files | Size | simple-grib (Go) | eccodes (C) | Notes |
|---|---:|---:|---:|---:|---|
| **JPEG 2000 (5.40)** CMA LARP | 4 | 153.3 MB | 3.5 MB/s | **9.3 MB/s** | pure-Go JPC decoder vs Jasper C — currently the main gap |
| **Simple packing, large files** ICON/pangu | 5 | 139.1 MB | **681 MB/s** | 678 MB/s | on par (≈1.00×) |
| GFS pgrb2 multi-field | 3 | 34.4 MB | **453 MB/s** | 229 MB/s | Go ≈2.0× faster |
| JMA GPV multi-field | 9 | 19.5 MB | 246 MB/s | **428 MB/s** | eccodes ≈1.7× faster (incl. a multi-field-slice protocol difference) |
| ECMWF small files (agl/atmosphere/CCSDS…) | 21 | 3.8 MB | **326 MB/s** | 74 MB/s | Go ≈4.4× faster (per-message overhead path) |
| **Total** | 42 | 350.1 MB | 8.0 MB/s · 11.1M val/s | **20.7 MB/s · 28.7M val/s** | overall throughput: eccodes ≈2.6× (dragged down by the JPC files) |

fail-loud evidence on the mixed corpus (before GRIB1 landed): all 5 non-GRIB2
files were rejected with context — GRIB edition 1 ×2, BUFR ×2 (magic `BUFR`),
zero-byte truncation ×1; failed files are never silently dropped, they are
reported along with the results. (GRIB1 is now supported; edition 3 / BUFR
are still rejected this way.)

To reproduce (from the repo root):

```sh
CGO_ENABLED=0 go run ./bench/p7_decode_bench --rounds 10 <files...> > go_results.jsonl
python3 -u bench/p7_decode_bench/eccodes_bench.py --rounds 10 <files...> > py_results.jsonl
```

Protocol notes: the simple-grib column is the pure data-values path (lazy
coordinate generation is not counted); eccodes calls the C API per message
through its Python binding (wrapper overhead inside the timed path); the JMA
GPV multi-field wrap decodes against prebuilt per-field slices on the eccodes
side (that binding build has no next-message API).

## Design

Key principles (spec-as-data / parse–interpret split / three-layer separation
/ graceful degradation / symmetric write):

1. **Spec-as-data** — the WMO CSVs are the single source of truth;
 `cmd/specgen` generates `spec/tables_gen.go` + `templates_gen.go`
 (provenance-stamped), `-check` detects drift; every WMO revision =
 re-sync + regenerate, with an auditable workflow.
2. **Parse–interpret split** — one generic template engine (`Walk`) parses all
 254 templates from the generated descriptors; semantic interpretation
 (grid coordinates, packing decode) is implemented per template number only
 where needed; unimplemented parts expose raw fields instead of failing.
3. **Three-layer separation** — L1 wire format (sections/bytes/bits) →
 L2 structure (templates/code tables, pure data) → L3 semantics
 (grid/pack/Product); each layer testable in isolation.
4. **Graceful degradation is a first-class citizen** — unknown template
 numbers, local-use values → typed accessors return contextual errors;
 raw fields are always reachable; never panics.
5. **Symmetric writing** — the same `TemplateDef` descriptors drive both
 reading and writing (`gribw`), so two structural definitions cannot drift.

```
L3 semantics grid/ (grid → coords) pack/ (data representation → values, registry + built-in decoders) (root) Product/Identification
L2 structure spec: WMO CSV ──specgen──► Go tables/template descriptors; Table.Lookup / Template / Walk
L1 wire format File (io.ReaderAt + message index) → Message (whole-message buffer) → Section (zero-copy slices); big-endian reads / BitReader
```

## Correctness

- **Real-corpus bit-level comparison**: CMA LARP four-anchor 442 messages
 (358M values) + data_snd 11 files 1109 messages (788.8M values, including
 CCSDS 156/156 and simple packing 69/69) match eccodes point-by-point on
 SHA-256; JPEG 2000 additionally cross-checked against Jasper's own decoder.
- **eccodes binary comparison vectors** (`testdata/ecc-xcheck/`): simple
 packing 576/576, IEEE 108/108 (incl. ±Inf/NaN/subnormals),
 regular/Gaussian/rotated grids point-by-point max diff = 0 / 4.26e-14° /
 9.9e-7°.
- **CCSDS 5.42 oracle**: 36/36 PASS (`scripts/xcheck/ccsds-oracle.sh`, digest
 anchors in the script header comments).

## Known limitations

- Data templates 5.1 / 5.41 PNG / 5.5x spectral / 5.200 run-length →
 `pack.ErrUnsupported` (fail-loud)
- IEEE 5.4 only precision=1 (binary32); 64/128-bit → fail-loud
- Bitmap indicator 1–254 (centre-defined) → fail-loud
- Grids: remaining projection templates (3.10/3.12/3.31…) and stretched
 Gaussian (3.42/3.43) are metadata-only; `Point/Lats/Lons` →
 `grid.ErrNotImplemented`
- Writers currently support 5.0 / 5.4(precision=1) (GRIB2) and **`gribw1`:
 GRIB1 simple packing** (grid types 0/4, bitmap, constant fields;
 round-trip verified against real files); BUFR is only recognised and
 rejected

## Documentation

| Doc | Contents |
|---|---|
| [wasm/README.md](wasm/README.md) | WASM entry: build, JS API, browser viewer, measured cross-arch compatibility |
| [bench/p7_decode_bench/README.md](bench/p7_decode_bench/README.md) | benchmark driver and protocol |

---

## AI powered

Model credits:

- **Powered by [GLM-5.3 Flash](https://z.ai)** — special thanks to
 [OpenCode](https://opencode.ai) for the free token quota that made this
 development possible
- **[Qwen 3.8 Flash](https://github.com/QwenLM)** — open-source model,
 locally deployed
- **[WorkBuddy](https://www.workbuddy.cn)**
