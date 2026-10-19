package grid

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// 二进制差分对拍：把本包的坐标生成器与 Linux 对拍机上的 eccodes 2.49.0
// 逐点比对。testdata/ecc-xcheck/ 中的向量存放 eccodes 自身导出的逐点
// 纬度/经度（grib_get_double_array "latitudes"/"longitudes"），对象是已
// 完整编码的 GRIB2 报文；此外还有 grib_get_gaussian_latitudes(trunc)
// 导出的原始高斯纬度列表，作为 legendreRoots / gaussianColatitudes 的
// 真值基准。
//
// Binary differential cross-checks of the coordinate generators against
// eccodes 2.49.0 (the Linux bench host). Vectors in testdata/ecc-xcheck/
// store eccodes' own per-point latitudes/longitudes
// (grib_get_double_array "latitudes"/"longitudes") for fully encoded
// GRIB2 messages, plus the raw Gaussian latitude list from
// grib_get_gaussian_latitudes(trunc) — the ground truth for legendreRoots
// / gaussianColatitudes.

const xcheckDir = "../testdata/ecc-xcheck"

// xcheckGrid 描述一个规则（经纬度）网格的对拍向量。
//
// xcheckGrid describes a cross-check vector for a regular (lat/lon) grid.
type xcheckGrid struct {
	Kind              string         `json:"kind"`
	Extra             map[string]int `json:"extra"`
	NumValues         int            `json:"numValues"`
	EccodesLatitudes  []float64      `json:"eccodes_latitudes"`
	EccodesLongitudes []float64      `json:"eccodes_longitudes"`
}

// xcheckGauss 描述一个高斯网格的对拍向量。
//
// xcheckGauss describes a cross-check vector for a Gaussian grid.
type xcheckGauss struct {
	Kind                     string    `json:"kind"`
	Ni, Nj, Trunc            int       // JSON 键 "Ni"/"Nj"/"trunc" 大小写不敏感匹配 / JSON keys "Ni"/"Nj"/"trunc" match case-insensitively
	EccodesGaussianLatitudes []float64 `json:"eccodes_gaussian_latitudes"`
}

func readXcheckFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(xcheckDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

// buildRegular 把 eccodes 的访问器值（微度为单位）换算成 regularParams
// 结构，模拟 T6b Definition 层将来的做法：经纬度与步长除以 1e6 转成度，
// 扫描标志位按 0/1 值置入 ScanMode。
//
// buildRegular converts eccodes accessor values (microdegrees) into a
// regularParams, mirroring what the T6b Definition layer will do: lat/lon
// and increments are divided by 1e6 into degrees, and the scan flags are
// folded into ScanMode when their value is 1.
func buildRegular(extra map[string]int) *regularParams {
	p := &regularParams{
		La1: float64(extra["latitudeOfFirstGridPoint"]) / 1e6,
		Lo1: float64(extra["longitudeOfFirstGridPoint"]) / 1e6,
		Di:  float64(extra["iDirectionIncrement"]) / 1e6,
		Dj:  float64(extra["jDirectionIncrement"]) / 1e6,
		Ni:  extra["Ni"],
		Nj:  extra["Nj"],
	}
	if extra["iScansNegatively"] == 1 {
		p.ScanMode |= scanNegI
	}
	if extra["jPointsAreConsecutive"] == 1 {
		p.ScanMode |= scanJFast
	}
	if extra["jPointsArePositively"] == 1 {
		p.ScanMode |= scanPosJ
	}
	return p
}

// TestXcheckGridRegular 把规则网格生成器与 eccodes 逐点对拍：对
// testdata/ecc-xcheck/ 下的 grid_a.json、grid_b.json 各自解码出参数、
// 生成格点，逐点与 eccodes 的纬经度比较，容差 1e-6 度；点数必须与
// numValues 一致。
//
// TestXcheckGridRegular cross-checks the regular-grid generator point by
// point against eccodes: for grid_a.json and grid_b.json under
// testdata/ecc-xcheck/ it decodes the parameters, generates the grid
// points, and compares each latitude/longitude against eccodes within
// 1e-6 degrees; the point count must equal numValues.
func TestXcheckGridRegular(t *testing.T) {
	for _, name := range []string{"grid_a.json", "grid_b.json"} {
		var v xcheckGrid
		if err := json.Unmarshal(readXcheckFile(t, name), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v.Kind != "grid" {
			t.Fatalf("%s: kind %q", name, v.Kind)
		}

		p := buildRegular(v.Extra)
		pts, err := p.points()
		if err != nil {
			t.Fatalf("%s: points: %v (scanMode=%d)", name, err, p.ScanMode)
		}
		if len(pts) != v.NumValues || len(pts) != len(v.EccodesLatitudes) {
			t.Fatalf("%s: got %d points, eccodes %d/%d", name, len(pts), v.NumValues, len(v.EccodesLatitudes))
		}

		maxLat, maxLon := 0.0, 0.0
		for i := range pts {
			dl := math.Abs(pts[i].Lat - v.EccodesLatitudes[i])
			do := math.Abs(pts[i].Lon - v.EccodesLongitudes[i])
			if dl > maxLat {
				maxLat = dl
			}
			if do > maxLon {
				maxLon = do
			}
			if dl > 1e-6 || do > 1e-6 {
				t.Errorf("%s: point %d: got (%.9f, %.9f), eccodes (%.9f, %.9f)",
					name, i, pts[i].Lat, pts[i].Lon, v.EccodesLatitudes[i], v.EccodesLongitudes[i])
			}
		}
		t.Logf("%s: scanMode=%d Ni=%d Nj=%d: max lat diff %.2e deg, max lon diff %.2e deg",
			name, p.ScanMode, p.Ni, p.Nj, maxLat, maxLon)
	}
}

// TestXcheckGaussian 把 gaussianColatitudes 的结果与 eccodes 的
// grib_get_gaussian_latitudes 列表对拍：余纬转回纬度后逐点比较，容差
// 1e-6 度。
//
// TestXcheckGaussian cross-checks gaussianColatitudes against the
// grib_get_gaussian_latitudes list from eccodes: colatitudes are converted
// back to latitudes and compared point by point within 1e-6 degrees.
func TestXcheckGaussian(t *testing.T) {
	var v xcheckGauss
	if err := json.Unmarshal(readXcheckFile(t, "gauss_a.json"), &v); err != nil {
		t.Fatal(err)
	}
	if v.Kind != "gaussian" {
		t.Fatalf("kind %q", v.Kind)
	}

	colats, err := gaussianColatitudes(v.Nj)
	if err != nil {
		t.Fatalf("gaussianColatitudes(%d): %v", v.Nj, err)
	}
	if len(colats) != len(v.EccodesGaussianLatitudes) {
		t.Fatalf("got %d colatitudes, eccodes %d latitudes", len(colats), len(v.EccodesGaussianLatitudes))
	}

	const deg = 180.0 / math.Pi
	maxDiff := 0.0
	for i := range colats {
		lat := 90.0 - colats[i]*deg
		d := math.Abs(lat - v.EccodesGaussianLatitudes[i])
		if d > maxDiff {
			maxDiff = d
		}
		if d > 1e-6 {
			t.Errorf("gauss row %d: got lat %.9f, eccodes %.9f (diff %.2e deg)", i, lat, v.EccodesGaussianLatitudes[i], d)
		}
	}
	t.Logf("gauss_a: Nj=%d trunc=%d: max latitude diff %.2e deg", v.Nj, v.Trunc, maxDiff)
}
