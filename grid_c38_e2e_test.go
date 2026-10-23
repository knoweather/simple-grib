//
// C38 协同终验 E2E 包。样本：testdata/gribw-xcheck/hrrr_conus_apcp_c38_t30.grib2
// （HRRR conus APCP f00，grid template 30 Lambert conformal、单标准纬圈退化分支
// LaD=Latin1=Latin2=38.5°、LoV=262.5°、Dx=Dy=3km、scanMode=64、Ni×Nj=1799×1059、
// simple packing 全零场；md5 = 8d69916c12608a546bb6366edc3e5039，取样台账 C38）。
//
// 本测试是 TestB_GridT30Baseline（ 前 fail-loud 基线）的翻转目标：
// build-end（A）时删除下方 env gate 即生效，同时按基线头注清单翻掉基线断言。
//
// 值半区（双验之一）：全零场——eccodes 2.41 grib_get_data 唯一值 = 0.0（distinct=1），
// B b5e20a3③ 已二次实证；本测试逐点断言 1,905,141 值全 0。
//
// 坐标半区锚——参考列（eccodes 2.41 @ the Linux bench host，2026-10-06 提取/复核；
// M #88 终裁：参考列原锚保留、oracle 重放为硬门）：
//   n = 1,905,141（= 1799×1059；Point(i) 扫描序 == eccodes grib_get_data 存储序）
//   参考列 text digest（oracle 口径 "%.10f,%.10f;" concat sha256，重放锚）:
//     1a76ed119dd37ef0a7902a1fdcebb0854af76f5573d6b843627f6da4140bc038
//   参考列 packed float64 BE sha256（本测试不断言；由 oracle 重放 + spot check 守护）:
//     lats = e7e83427cb7a9abbac52101c5d2b0f9bee9cca3156ac9658967a66db985ad5b8
//     lons = 8c43b60a10e552d20ce5333627e2840931f1f225fda76bcfdd4f6e011c6505fc
//   首/中/末三点 spot check（%.10f / repr 双口径，mid_idx = n/2 = 952570）:
//     i=0        lat 21.1381230000 (=21.138123000000004)   lon 237.2804720000 (=237.28047200000003)
//     i=952570   lat 38.4972466510 (=38.49724665100054)    lon 262.4940233090 (=262.494023308958)
//     i=1905140  lat 47.8421950225 (=47.84219502248866)    lon 299.0828072282 (=299.0828072281622)
//   范围：lat [21.138123, 52.615653]、lon [225.904520, 299.082807]（[0,360) 口径）
//
// 口径注记（ build-end A2 2026-10-07；双列锚——M #88 终裁准，五项条件见板面帖）：
// 参考列（eccodes 2.41 @ the Linux bench host，）= 上方 text/packed digest，
// oracle 重放口径保留（grid-t20t30-oracle.py @ the Linux bench host；M 验收独立复跑）。
//   Go 列（本测试断言，字节硬门不得降级为容差——#88②b)）= A2 实现（公式逐句镜像
//     eccodes 2.41 源，C 侧独立验证 gcc+glibc 全网格 0/1,905,141 点与 eccodes dump
//     位级一致；C 验证程序 sha256/复跑命令见板面 A2 补充帖①）的 packed f64 BE
//     sha256，锚定工具链 go1.27.1（2026-10-13 重锚；go1.25.4 / go1.26.5 / go1.27.1
//     三版实测同值）：
//       lats = 70618c3cc46955b32c8c4ed7389480b3b95e1216fd8e24c846a257de94457452
//       lons = e1c1917a1a4441e4086c7c2ebdf7d63c076b6d223eab1a7be7a027f6d6e347ba
//   重锚记（2026-10-13，用户直令「全体换 1.27.1」）：旧 Go 列锚（A2 2026-10-07 预计算、
//     go1.23.4 ≡ go1.24.4 实测同值：lats=59036630ff4d8556efa1e0dbc63a0ec7efe709baa37fda0b35fdd8983cb017cf、
//     lons=b71ed5c9fa7b6c58b51abbbdb1d41729bc7da99957fe8720650c7bfa6b6ab6b8）在 go1.25+ 上失配——
//     Go 1.25 起标准库 math（tan/pow）与 1.23/1.24 存在 1-ulp 级分歧，沿 init 常数
//     f/rho/rho0 传播。go1.23.4 vs go1.27.1 全网格逐点实测：lats 1,366,516/1,905,141 点
//     末位不同（max 28 ulp、max|Δlat|=9.9e-14°）、lons 323,079 点（max 2 ulp、
//     max|Δlon|=5.7e-14°）——仍远小于 1e-9° spot 容差，数值正确性不受影响；按用户指令
//     Go 列锚整体换为 go1.27.1 口径，eccodes 参考列（上方 text/packed digest）原锚保留。
//   两列差异根因（A2 校准证据，板面 build-end 帖③ + 补充帖，2026-10-07；下述点数/量级
//     系 go1.23.4 口径实测，eccodes/glibc 参考列口径不变）：Go math 与 glibc libm 在
//     tan/pow 个别输入上 1-ulp 分歧（init 常数 f/rho/rho0_bare 传播），C38 有
//     953,709/1,905,141 点末位 ulp 级不同，max|Δlat|=7.8e-14°、max|Δlon|=5.7e-14°
//     ——远小于 spot check 容差 1e-9° 与 P4-B3 兜底口径 1e-6°；纯 Go（CGO_ENABLED=0
//     红线）无法复刻 glibc libm 的舍入，故位级硬门按 Go 列锚定，eccodes 参考列由
// spot check（本测试）+ oracle 重放（the Linux bench host）交叉守护。
//   spot check 用 1e-9° 容差作诊断层（原口径保留）。

package grib

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
)

func TestB_C38EndToEnd(t *testing.T) {
	fl, err := Open("testdata/gribw-xcheck/hrrr_conus_apcp_c38_t30.grib2")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer fl.Close()
	m, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}

	const wantNV = 1905141 // 1799 x 1059（sec3 declared， 后 computed 应相等）

	// ── 值半区：全零场（双验之一；eccodes 唯一值 = 0.0）──
	vals, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(vals) != wantNV {
		t.Fatalf("len(values)=%d, want %d", len(vals), wantNV)
	}
	for i, v := range vals {
		if v != 0 {
			t.Fatalf("value[%d]=%v；C38 为全零场（eccodes grib_get_data distinct=1, value=0.0）", i, v)
		}
	}

	// ── 坐标半区：Point(i) 扫描序迭代 == eccodes 存储序 ──
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	if gp := g.NumPoints(); gp != wantNV {
		t.Fatalf("NumPoints=%d, want %d（declared=%d）", gp, wantNV, g.NumPointsDeclared())
	}
	lats := make([]float64, wantNV)
	lons := make([]float64, wantNV)
	for i := 0; i < wantNV; i++ {
		la, lo, err := g.Point(i)
		if err != nil {
			t.Fatalf("Point(%d): %v", i, err)
		}
		lats[i], lons[i] = la, lo
	}

	// 首/中/末三点 spot check（诊断层，1e-9° 容差）
	spot := []struct {
		idx      int
		lat, lon float64
	}{
		{0, 21.138123000000004, 237.28047200000003},
		{952570, 38.49724665100054, 262.494023308958},
		{wantNV - 1, 47.84219502248866, 299.0828072281622},
	}
	for _, s := range spot {
		dLat := math.Abs(lats[s.idx] - s.lat)
		dLon := math.Abs(lons[s.idx] - s.lon)
		if dLat > 1e-9 || dLon > 1e-9 {
			t.Fatalf("spot[%d] = (%.10f, %.10f), want (%.10f, %.10f); delta=(%g, %g)",
				s.idx, lats[s.idx], lons[s.idx], s.lat, s.lon, dLat, dLon)
		}
		t.Logf("spot[%d] = (%.10f, %.10f) delta=(%g, %g)", s.idx, lats[s.idx], lons[s.idx], dLat, dLon)
	}

	// packed float64 BE sha256 位级硬门（Go 列锚；2026-10-13 按 go1.27.1 重锚，
	// go1.25.4/1.26.5/1.27.1 实测同值。eccodes 参考列见头注双列口径。）
	if got := f64beDigest(t, lats); !knownLatsDigest(got) {
		t.Fatalf("lats packed f64 BE sha256 = %s, want 70618c3c…（Go 列锚, go1.27.1）", got)
	}
	if got := f64beDigest(t, lons); !knownLonsDigest(got) {
		t.Fatalf("lons packed f64 BE sha256 = %s, want e1c1917a…（Go 列锚, go1.27.1）", got)
	}
}

// f64beDigest: float64 切片按大端字节序打包后取 sha256（与 B c323628 / oracle 头注同口径）。
func f64beDigest(t *testing.T, a []float64) string {
	t.Helper()
	buf := make([]byte, len(a)*8)
	for i, x := range a {
		binary.BigEndian.PutUint64(buf[i*8:], math.Float64bits(x))
	}
	sum := sha256.Sum256(buf)
	return fmt.Sprintf("%x", sum)
}
