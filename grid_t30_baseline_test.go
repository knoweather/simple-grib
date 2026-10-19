//
// baseline — FLIPPED at A build-end per the header checklist below.
// HRRR conus APCP f00 — the final-E2E acceptance sample ("HRRR APCP
// C38 型端到端值级交叉"). Real file: grid template 30 (Lambert conformal,
// single standard parallel LaD=Latin1=Latin2=38.5° — the degenerate n=sin(Latin1)
// branch), LoV=262.5°, Dx=Dy=3km, scanMode=64, Ni×Nj=1799×1059, simple packing,
// ALL-ZERO field (verified: grib_get_data unique value = 0.0 over all
// 1,905,141 points). md5 = 8d69916c12608a546bb6366edc3e5039 (sampling ledger C38).
// Coordinate anchors — 双列锚（M #88 终裁准；每列内部字节级硬断言，跨列三层守护）：
// 参考列（eccodes 2.41 @ the Linux bench host；oracle 重放为硬门）:
//     grid-t20t30-oracle.py text digest ("%.10f,%.10f;" concat) sha256 =
//       1a76ed119dd37ef0a7902a1fdcebb0854af76f5573d6b843627f6da4140bc038
//     packed float64 BE (Go-side convention, recorded 2026-10-06):
//       sha256(Lats as f64 BE) = e7e83427cb7a9abbac52101c5d2b0f9bee9cca3156ac9658967a66db985ad5b8
//       sha256(Lons as f64 BE) = 8c43b60a10e552d20ce5333627e2840931f1f225fda76bcfdd4f6e011c6505fc
//     Point(0) = (lat 21.138123, lon 237.280472) = La1/Lo1 declared in sec3.
//   Go 列锚（本测试断言，字节硬门不得降级——#88②b)；2026-10-13 按 go1.27.1 重锚，
//   go1.25.4 / go1.26.5 / go1.27.1 三版实测同值）:
//     sha256(Lats as f64 BE) = 70618c3cc46955b32c8c4ed7389480b3b95e1216fd8e24c846a257de94457452
//     sha256(Lons as f64 BE) = e1c1917a1a4441e4086c7c2ebdf7d63c076b6d223eab1a7be7a027f6d6e347ba
//   重锚记（2026-10-13，用户直令「全体换 1.27.1」）：A2 2026-10-07 旧 Go 列锚
//   （go1.23.4 ≡ go1.24.4 实测同值：lats=59036630…、lons=b71ed5c9…）在 go1.25+ 上失配——
//   Go 1.25 起标准库 math（tan/pow）与 1.23/1.24 有 1-ulp 级分歧；go1.23.4 vs
//   go1.27.1 全网格逐点实测 max|Δlat|=9.9e-14°、max|Δlon|=5.7e-14°，数值正确性不受影响。
//   双列差异根因（下述点数/量级系 go1.23.4 口径实测）：Go math 与 glibc libm 个别 tan/pow
//   输入 1-ulp 分歧（公式逐句镜像已验证——C 侧 gcc+glibc 全网格 0/1,905,141 点与 eccodes
//   dump 位级一致），C38 max|Δlat|=7.8e-14°、max|Δlon|=5.7e-14°，远小于 1e-9° spot 容差。
//   详见 grid_c38_e2e_test.go 头注与板面帖（A 2026-10-07）。
//
// PRE- behavior (historical record): grid template 30 was kindNone
// (metadata only, ). Values() refused via the declared-vs-computed
// NV cross-check ("declares 1905141 data points but the grid definition
// computes 0"); Point/Lats/Lons failed with *grid.ErrNotImplemented{Template: 30}.
//
// POST- (this file, flipped): ① Values() returns all-zero values over
// 1905141 points (the value half of the final-E2E cross); ② coordinate golden
// checks — Lats()/Lons() length = 1905141 (full Ni×Nj per-point arrays,
// revision), packed float64 BE sha256 vs the Go-column
// anchor above, and Point(0) within 1e-9° of the eccodes replay first point
// (lon bit-exact; lat 1-ulp off, see dual-column note). Same pattern as
// falloud_baseline_test.go (B baseline -> A golden flip at / build-ends).

package grib

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestB_GridT30Baseline(t *testing.T) {
	fl, err := Open("testdata/gribw-xcheck/hrrr_conus_apcp_c38_t30.grib2")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fl.Close()
	m, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}

	dr, err := m.DataRep()
	if err != nil {
		t.Fatalf("DataRep: %v", err)
	}
	const wantNV = 1905141 // 1799 x 1059
	if dr.TemplateNumber != 0 || dr.NumValues != wantNV {
		t.Fatalf("tmpl=%d NV=%d, want tmpl=0 (simple) NV=%d", dr.TemplateNumber, dr.NumValues, wantNV)
	}

	// ① Value half of the final-E2E cross: all 1,905,141 points exactly zero.
	vals, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(vals) != wantNV {
		t.Fatalf("len(values)=%d, want %d", len(vals), wantNV)
	}
	for i, v := range vals {
		if v != 0 {
			t.Fatalf("value[%d]=%v; C38 is the all-zero field (eccodes distinct=1, value=0.0)", i, v)
		}
	}

	// ② Coordinate golden checks (: template 30 implemented).
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if gp := g.NumPoints(); gp != wantNV {
		t.Fatalf("NumPoints=%d, want %d (declared=%d)", gp, wantNV, g.NumPointsDeclared())
	}
	lats, err := g.Lats()
	if err != nil {
		t.Fatalf("Lats: %v", err)
	}
	lons, err := g.Lons()
	if err != nil {
		t.Fatalf("Lons: %v", err)
	}
	if len(lats) != wantNV || len(lons) != wantNV {
		t.Fatalf("Lats/Lons len = %d/%d, want %d (full Ni×Nj per-point arrays)", len(lats), len(lons), wantNV)
	}

	// Point(0) = first point of the eccodes replay (La1/Lo1): 1e-9° tolerance.
	la0, lo0, err := g.Point(0)
	if err != nil {
		t.Fatalf("Point(0): %v", err)
	}
	if dLat := math.Abs(la0 - 21.138123); dLat > 1e-9 {
		t.Fatalf("Point(0).lat = %.10f, want 21.138123 (delta %g)", la0, dLat)
	}
	if lo0 != 237.28047200000003 { // bit-exact vs eccodes replay
		t.Fatalf("Point(0).lon = %.17g, want 237.28047200000003 (bit-exact)", lo0)
	}

	// packed float64 BE sha256 — Go-column anchor (dual-column convention,
	// see header; eccodes reference column replayable via the oracle script).
	// 2026-10-13 重锚 go1.27.1（go1.25.4/1.26.5/1.27.1 实测同值）。
	if got := baselineF64beDigest(lats); got != "70618c3cc46955b32c8c4ed7389480b3b95e1216fd8e24c846a257de94457452" {
		t.Fatalf("lats packed f64 BE sha256 = %s, want 70618c3c… (Go-column anchor, go1.27.1)", got)
	}
	if got := baselineF64beDigest(lons); got != "e1c1917a1a4441e4086c7c2ebdf7d63c076b6d223eab1a7be7a027f6d6e347ba" {
		t.Fatalf("lons packed f64 BE sha256 = %s, want e1c1917a… (Go-column anchor, go1.27.1)", got)
	}

	// sanity: Lats/Lons[i] == Point(i) for a few indices (per-point consistency).
	for _, i := range []int{0, 1, wantNV / 2, wantNV - 1} {
		la, lo, err := g.Point(i)
		if err != nil {
			t.Fatalf("Point(%d): %v", i, err)
		}
		if lats[i] != la || lons[i] != lo {
			t.Fatalf("Lats/Lons[%d]=(%g,%g) != Point(%d)=(%g,%g)", i, lats[i], lons[i], i, la, lo)
		}
	}
}

func baselineF64beDigest(a []float64) string {
	buf := make([]byte, len(a)*8)
	for i, x := range a {
		binary.BigEndian.PutUint64(buf[i*8:], math.Float64bits(x))
	}
	sum := sha256.Sum256(buf)
	return fmt.Sprintf("%x", sum)
}
