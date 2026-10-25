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

<p align="center"><strong>English</strong> | <a href="./README.zh-CN.md">简体中文</a></p>

Pure-Go **GRIB edition 1 + 2** reader/writer — zero cgo, no eccodes, no C
toolchain. The only non-standard-library module is the pure-Go
[`simple-webp`](https://github.com/knoweather/simple-webp) decoder behind the
local-use WebP template. Metadata
tracks ecCodes key by key, decoded values are bit-exact, and the same code builds
to WebAssembly.

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

- **ecCodes-compatible metadata**: `shortName` / `name` / `paramId` / `units` /
  `typeOfLevel` / `parameterName` are answered by the concept chain generated from
  the vendored ecCodes definitions (local centre → master → ecmf → legacy, ties to
  master; table 4.2 and table 2 lookups take the message's own table version) —
  not by a hand-written map.
- **Bit-exact values**: CMA LARP four anchors, 442 messages (358M values) +
  data_snd 11 files, 1109 messages (788.8M values, incl. CCSDS 156/156 and simple
  packing 69/69) hash-identical to eccodes; JPEG 2000 is cross-checked against
  Jasper's own decoder.
- **Editions 1 and 2, one API**: `grib.Open` dispatches by edition, GRIB1 keeps
  native `float64` precision (`Message.Values64()`), and one file may mix both.
- **WASM ready**: `wasm/` builds `grib.wasm` plus a browser viewer — no server,
  no C toolchain (`CGO_ENABLED=0 go build ./...` passes).

> [!NOTE]
> 🚀 **Initial release, canary testing in progress.** If you hit any problem, please open an [issue](https://github.com/knoweather/simple-grib/issues)!

A sibling project of [simple-netcdf](https://github.com/knoweather/simple-netcdf).

## GRIB1

Landed 2026-10-15. `grib.Open` dispatches automatically by edition — GRIB1
messages are parsed by the `grib1/` package (five sections IS/PDS/GDS/BMS/BDS,
IBM-float reference value, sign-magnitude scale factors, bitmap expansion,
simple-packed bit stream), reusing the grid-coordinate generators already
verified on the GRIB2 side (regular lat/lon, Gaussian, rotated lat/lon, polar
stereographic, Lambert conformal, quasi-regular);
GRIB1 and GRIB2 messages may be mixed within one file. GRIB1 values keep
native float64 precision (`Message.Values64()`). `Product.ParameterName` resolves
against the vendored per-(centre, table version) table 2 — same selection as
ecCodes' `grib1/2.[centre].[table2Version].table` accessor: `table2Version <
128` routes to the WMO tables (the centre segment is ignored), `>= 128` uses
the message's centre (`subCentre == 98` taking over when the centre is not
98), and a missing table or code degrades to the decimal code string exactly
like ecCodes' `codetable_title`; `grib1.RegisterTable2` still overrides it.

## Features

| Capability | Status |
|---|---|
| Multi-message file index + section 0–7 structural parsing and validation (GRIB2) | ✅ |
| **GRIB1 decoding**: five-section parse + values (native float64) + coordinates; mixable with GRIB2 | ✅ |
| Metadata: Identification / Product (with WMO code-table names), `Walk` generic template walker (one parser for all 254 templates) | ✅ |
| Grid coordinates: 3.0/3.1/3.2/3.3 (+rotated), 3.40/3.41 Gaussian, **3.20 polar stereographic / 3.30 Lambert conformal point-by-point** | ✅ |
| Data decoding: 5.0 simple (any bit width), 5.2 complex, 5.3 complex+spatial differencing, 5.4 IEEE binary32, **5.40 JPEG 2000 (pure-Go JPC decoder, 5/3 + 9/7)**, **5.41 PNG (grayscale 1/2/4/8/16-bit)**, 5.42 CCSDS AEC (own codec); 5.49152 carries a WebP image payload (local use, stored verbatim; header/decode via `simple-webp`) | ✅ |
| Bitmap (section 6, indicator 0/255) + declared point count cross-check | ✅ |
| Writers: `gribw` templates 5.0 / 5.4 with round-trip tests, plus 5.41 PNG (`SetPNG`) and 5.49152 WebP (`SetWebP`) images; local table registration | ✅ |
| Writer `gribw1`: GRIB1 encoding (simple packing, grid types 0/4 + bitmap + constant fields) | ✅ |
| CLI `gribdump`: metadata / values / field-by-field `Walk` / JSON / WebP image export | ✅ |
| **WASM** `wasm/`: decode GRIB1 + GRIB2 directly in the browser / Node; metadata + values + coordinates as TypedArrays, drag-and-drop viewer included | ✅ |

## Install

```bash
go get github.com/knoweather/simple-grib
```

Prebuilt `gribdump` binaries for linux/amd64, linux/arm64, linux/loong64,
windows/amd64 and darwin/arm64 are attached to each
[release](https://github.com/knoweather/simple-grib/releases).

## Quick start

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test -count=1 ./...

go run ./cmd/gribdump sample.grib2 # per-message metadata (with code-table names)
go run ./cmd/gribdump -values 16 sample.grib2 # first 16 decoded values
go run ./cmd/gribdump -section 3 -section 5 sample.grib2 # field-by-field Walk
go run ./cmd/gribdump -json sample.grib2 # machine readable
go run ./cmd/gribdump -webp satellite.webp sample.grib2 # extract a WebP image payload
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

### WebP image payload (local use)

GRIB can also carry an image product: template `5.49152` stores a complete WebP
file verbatim — the library neither decodes pixels on write nor re-encodes
values into an image. `Values()` reports `pack.ErrNotNumeric` for such messages;
use the image accessors instead.

```go
webp, _ := os.ReadFile("satellite.webp")

msg := gribw.New()
_ = msg.SetDiscipline(0)
_ = msg.SetIdentification(id)
_ = msg.SetGrid(g)
_ = msg.SetProduct(prod)
_ = msg.SetWebP(webp)   // section 5 = 11-byte header, section 7 = the file verbatim
data, _ := gribw.File(msg)

m, _ := fl.Message(0)   // reader side
raw, _ := m.WebP()      // identical bytes back
info, _ := m.WebPInfo() // {Width, Height, Lossless, Animated}; header only
img, _ := m.WebPImage() // decoded image.Image (first frame if animated)
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
for every package, and CI runs the whole suite under Node on Linux. Coordinate
anchors are gated per platform: the packed-float64 digests stay bit-exact on
the recorded amd64 and darwin/arm64 builds, while other platforms (wasm, other
GOARCH or GOAMD64 variants) must reproduce 192 sampled points within 1e-9°.
Host-only checks — spawning a native `go build` and fuzzing — skip on js/wasm;
js/wasm under a **Windows host** still cannot list directories (Linux hosts
can). Item-by-item evidence in
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

1. **Spec-as-data** — the WMO CSVs plus the vendored ecCodes definitions
 (see `paramdata/PROVENANCE.txt`) are the single source of truth;
 `cmd/specgen` generates `spec/tables_gen.go` + `templates_gen.go` +
 `paramname_gen.go` (the versioned 4.2 title pool), `cmd/paramgen` generates
 the concept / MARS / GRIB1 table-2 data (`paramdb/concepts_gen.go`,
 `paramdb/mars_gen.go`, `grib1/mars_gen.go`, `grib1/table2_gen.go`,
 `mars_gen.go`), and both have `-check` drift detection; every WMO/ecCodes
 revision = re-sync + regenerate, with an auditable workflow.
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
- **JPEG 2000 9/7 cross-check**: OpenJPEG-generated fixtures (8/16-bit, 0-3
 decomposition levels, odd dimensions) decode sample-for-sample identically to
 OpenJPEG; generator provenance in `testdata/jpc97/README.md`.
- **PNG 5.41 cross-checks**: encoder-independent fixtures from pypng, Pillow
 and the sister library simple-png (1/2/4/8/16-bit plus Adam7), and 500
 randomized 1/2/4/8/16-bit `SetPNG` outputs decoded by simple-png — every
 sample matches.

## Known limitations

- Data templates 5.1 / 5.5x spectral / 5.200 run-length →
 `pack.ErrUnsupported` (fail-loud)
- JPEG 2000 5.40: reversible 5/3 and irreversible 9/7, LRCP order, one layer
 and one component; multi-layer / multi-component codestreams and
 COC/QCC/RGN/POC/PPM/PPT/SOP/EPH markers → `pack.ErrJPCUnsupported`
- PNG 5.41: grayscale only, bit depth 1/2/4/8/16 and equal to
 `bitsPerValue`; colour, transparent and palette PNGs → fail-loud. `SetPNG`
 encodes a grayscale image from values; a pre-encoded PNG can still be
 installed with `SetDataSection`
- Local-use template 49152 (WebP) is an opaque image payload: stored and
 returned byte-for-byte by `SetWebP`/`WebP`, and `Values` reports
 `pack.ErrNotNumeric`. Lossy and animated files are accepted (`WebPImage`
 returns the first frame for animations); the image size need not match the
 grid, because `numberOfValues` records the pixel count
- IEEE 5.4 only precision=1 (binary32); 64/128-bit → fail-loud
- Bitmap indicator 1–254 (centre-defined) → fail-loud
- Grids: remaining projection templates (3.10/3.12/3.31…) and stretched
 Gaussian (3.42/3.43) are metadata-only; `Point/Lats/Lons` →
 `grid.ErrNotImplemented`
- Writers currently support 5.0 / 5.4(precision=1) (GRIB2), the 5.41 PNG
 (`SetPNG`, grayscale 1/2/4/8/16-bit) and local-use 5.49152 WebP image
 (`SetWebP`); and **`gribw1`:
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

## Credits

The Go gopher was designed by [Renee French](https://reneefrench.blogspot.com/) and is
licensed under [CC BY 3.0](https://creativecommons.org/licenses/by/3.0/). The logo in
[`docs/logo.svg`](docs/logo.svg) is a derivative work based on that design.
