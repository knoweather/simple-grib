package paramdb

import (
	"testing"

	"github.com/knoweather/simple-grib"
)

// 端到端：QueryFrom + 概念查询跑在真实样例上。锚点取自样例的已知参数
// （GFS 的 2 米温度、ECMWF GRIB1 的 228=tp）。
func TestQueryFromRealGRIB2GFS(t *testing.T) {
	f, err := grib.Open("../.m-realworld/gfs.t12z.pgrb2.0p25.f000")
	if err != nil {
		t.Skipf("GFS sample not available: %v", err)
	}
	defer f.Close()

	found2t := false
	n := 0
	for m, err := range f.Messages() {
		if err != nil {
			t.Fatal(err)
		}
		n++
		q, err := QueryFrom(m)
		if err != nil {
			t.Fatalf("message %d: QueryFrom: %v", m.Index(), err)
		}
		sn, _ := ShortName(q)
		p := mustProduct(t, m)
		if p.ParameterCategory == 0 && p.ParameterNumber == 0 &&
			p.Level1 != nil && p.Level1.Type == 103 && p.Level1.HasValue && p.Level1.Scale == 2 {
			if sn != "2t" {
				t.Errorf("GFS 2m temperature: ShortName = %q, want 2t", sn)
			}
			if u, ok := Units(q); !ok || u != "K" {
				t.Errorf("GFS 2m temperature: Units = %q,%v; want K", u, ok)
			}
			if tol, ok := TypeOfLevel(q); !ok || tol != "heightAboveGround" {
				t.Errorf("GFS 2m temperature: TypeOfLevel = %q,%v; want heightAboveGround", tol, ok)
			}
			found2t = true
		}
		if n > 200 {
			break // 样例足够大，前 200 条足以覆盖断言
		}
	}
	if n == 0 {
		t.Fatal("no messages")
	}
	if !found2t {
		t.Skip("sample has no 2m-temperature message in the first 200; nothing to assert")
	}
}

// DWD ICON：centre 78 (edzw) 且 masterTablesVersion=19（≤ 33，MTG2 的
// .lte33 分支）。edzw 目录没有 .lte33 变体，ecCodes 对所有表版本都用同一份
// 本地概念；若生成器给普通条目硬加 mtg2=1，这类消息会落回 master 的
// ccl/260257，而不是 DWD 的 CLCH/502341。
func TestQueryFromRealGRIB2DWD(t *testing.T) {
	const sample = "../.m-realworld/icon_global_icosahedral_single-level_2025092400_000_CLCH_a.grib2"
	f, err := grib.Open(sample)
	if err != nil {
		t.Skipf("DWD ICON sample not available: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatal(err)
	}
	q, err := QueryFrom(m)
	if err != nil {
		t.Fatal(err)
	}
	if sn, ok := ShortName(q); !ok || sn != "CLCH" {
		t.Errorf("DWD CLCH: ShortName = %q,%v; want CLCH,true", sn, ok)
	}
	if name, ok := Name(q); !ok || name != "Cloud Cover (0 - 400 hPa)" {
		t.Errorf("DWD CLCH: Name = %q,%v; want Cloud Cover (0 - 400 hPa),true", name, ok)
	}
	if id, ok := ParamID(q); !ok || id != 502341 {
		t.Errorf("DWD CLCH: ParamID = %d,%v; want 502341,true", id, ok)
	}
	if u, ok := Units(q); !ok || u != "%" {
		t.Errorf("DWD CLCH: Units = %q,%v; want %%,true", u, ok)
	}
	if tol, ok := TypeOfLevel(q); !ok || tol != "isobaricLayer" {
		t.Errorf("DWD CLCH: TypeOfLevel = %q,%v; want isobaricLayer,true", tol, ok)
	}
}

// ECMWF GRIB1（中心本地表 2.98.<table2Version>.table）：该样例的
// table2Version 是 173，其 228 号是 "tpara"（总降水异常累积率）——
// 换一张 ECMWF 表（如 128），同一个 228 号就是 "tp"。这正是中心本地
// 表必须按 (centre, table2Version) 圈定、绝不能跨表混用的原因。
func TestQueryFromRealGRIB1ECMWF(t *testing.T) {
	f, err := grib.Open("../testdata/step-range/g1_ecmwf_3h.grib1")
	if err != nil {
		t.Skipf("ECMWF GRIB1 sample not available: %v", err)
	}
	defer f.Close()
	m, err := f.Message(0)
	if err != nil {
		t.Fatal(err)
	}
	q, err := QueryFrom(m)
	if err != nil {
		t.Fatal(err)
	}
	p := mustProduct(t, m)
	if p.ParameterNumber != 228 {
		t.Skipf("first message is param %d, not 228; nothing to assert", p.ParameterNumber)
	}
	if sn, ok := ShortName(q); !ok || sn != "tpara" {
		t.Errorf("ECMWF GRIB1 param 228 (t2v=173): ShortName = %q,%v; want tpara,true", sn, ok)
	}
	if name, ok := Name(q); !ok {
		t.Error("ECMWF GRIB1 param 228: Name no match")
	} else if name != "Total precipitation anomalous rate of accumulation" {
		// name/paramId 来自 grib1/localConcepts/ecmf（centre .table 没有
		// paramId，name 也只是显示名）；ecCodes 给的就是这个措辞。
		t.Errorf("ECMWF GRIB1 param 228: Name = %q, want localConcepts wording", name)
	}
	if id, ok := ParamID(q); !ok || id != 173228 {
		t.Errorf("ECMWF GRIB1 param 228: ParamID = %d,%v; want 173228,true", id, ok)
	}
	if tol, ok := TypeOfLevel(q); !ok || tol != "surface" {
		t.Errorf("ECMWF GRIB1 param 228: TypeOfLevel = %q,%v; want surface,true", tol, ok)
	}
	if u, ok := Units(q); !ok || u != "m s**-1" {
		t.Errorf("ECMWF GRIB1 param 228: Units = %q,%v; want m s**-1", u, ok)
	}
}

func mustProduct(t *testing.T, m *grib.Message) *grib.Product {
	t.Helper()
	p, err := m.Product()
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	return p
}
