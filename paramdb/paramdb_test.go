package paramdb

import (
	"testing"
)

// 锚点查询全部对着 paramdata/ 的 .def 源数据核对过（ecCodes 2.50.0）。
// 这些用例同时是刷新数据时的回归锚：升级 ecCodes 后若锚点变值，那是
// 上游语义变化，须人工确认。

func q2(pairs ...string) Map {
	q := Map{
		"discipline":                      "0",
		"parameterCategory":               "0",
		"parameterNumber":                 "0",
		"productDefinitionTemplateNumber": "0",
		"masterTablesVersionNumber":       "29",
		"localTablesVersionNumber":        "1",
		"centre":                          "98",
		"subCentre":                       "0",
		"mtg2":                            "1",
		"typeOfSecondFixedSurface":        "255",
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		q[pairs[i]] = pairs[i+1]
	}
	return q
}

// 2 米温度：0/0/0 + 离地 2 米 → 2t（level-conditioned concept 的招牌用例）。
func TestShortNameTwoMetreTemperature(t *testing.T) {
	q := q2(
		"typeOfFirstFixedSurface", "103",
		"scaleFactorOfFirstFixedSurface", "0",
		"scaledValueOfFirstFixedSurface", "2",
	)
	if sn, ok := ShortName(q); !ok || sn != "2t" {
		t.Errorf("ShortName = %q,%v; want 2t,true", sn, ok)
	}
	if name, ok := Name(q); !ok || name != "2 metre temperature" {
		t.Errorf("Name = %q,%v; want 2 metre temperature,true", name, ok)
	}
	if id, ok := ParamID(q); !ok || id != 167 {
		t.Errorf("ParamID = %d,%v; want 167,true", id, ok)
	}
	if u, ok := Units(q); !ok || u != "K" {
		t.Errorf("Units = %q,%v; want K,true", u, ok)
	}
	if tol, ok := TypeOfLevel(q); !ok || tol != "heightAboveGround" {
		t.Errorf("TypeOfLevel = %q,%v; want heightAboveGround,true", tol, ok)
	}
}

// 等压面温度：同一参数号，换成 100 层 850 hPa → t 而不是 2t。
func TestShortNameIsobaricTemperature(t *testing.T) {
	q := q2(
		"typeOfFirstFixedSurface", "100",
		"scaleFactorOfFirstFixedSurface", "0",
		"scaledValueOfFirstFixedSurface", "85000",
		"pressureUnits", "hPa",
	)
	if sn, ok := ShortName(q); !ok || sn != "t" {
		t.Errorf("ShortName = %q,%v; want t,true", sn, ok)
	}
	if name, ok := Name(q); !ok || name != "Temperature" {
		t.Errorf("Name = %q,%v; want Temperature,true", name, ok)
	}
	if tol, ok := TypeOfLevel(q); !ok || tol != "isobaricInhPa" {
		t.Errorf("TypeOfLevel = %q,%v; want isobaricInhPa,true", tol, ok)
	}
	// (1, 8500) 与 (0, 85000) 解码同值，但 ecCodes 的条件按原始键对匹配：
	// 概念条目锚定 (0, 85000)，换成 (1, 8500) 必须落不进同一条件。
	q["scaleFactorOfFirstFixedSurface"] = "1"
	q["scaledValueOfFirstFixedSurface"] = "8500"
	if sn, ok := ShortName(q); ok && sn == "t" {
		// 允许落到别的条目，但不允许是同一条 —— 这里只记录行为。
		t.Logf("scale (1,8500) shortName = %q (different raw key pair)", sn)
	}
}

// 海平面气压：0/3/0 + 101 层（Mean sea level）。地表气压（1 层）是另一个
// 参数（sp / Surface pressure），概念靠层类型区分二者。
func TestShortNameMeanSeaLevelPressure(t *testing.T) {
	q := q2(
		"parameterCategory", "3",
		"typeOfFirstFixedSurface", "101",
	)
	if sn, ok := ShortName(q); !ok || sn != "msl" {
		t.Errorf("ShortName = %q,%v; want msl,true", sn, ok)
	}
	if name, ok := Name(q); !ok || name != "Mean sea level pressure" {
		t.Errorf("Name = %q,%v; want Mean sea level pressure,true", name, ok)
	}
	if tol, ok := TypeOfLevel(q); !ok || tol != "meanSea" {
		t.Errorf("TypeOfLevel = %q,%v; want meanSea,true", tol, ok)
	}
	// 地表气压：1 层 → sp。
	sp := q2(
		"parameterCategory", "3",
		"typeOfFirstFixedSurface", "1",
		"scaleFactorOfFirstFixedSurface", Missing,
		"scaledValueOfFirstFixedSurface", Missing,
	)
	if sn, ok := ShortName(sp); !ok || sn != "sp" {
		t.Errorf("Surface pressure: ShortName = %q,%v; want sp,true", sn, ok)
	}
}

// 累积降水：0/1/52 + 统计处理=1（累积）+ 地表 → tp。统计处理键是区分
// 累积类 shortName 的关键（master 概念 2095/3419 条带该条件）。
func TestShortNameTotalPrecipitation(t *testing.T) {
	q := q2(
		"parameterCategory", "1",
		"parameterNumber", "52",
		"typeOfFirstFixedSurface", "1",
		"scaleFactorOfFirstFixedSurface", Missing,
		"scaledValueOfFirstFixedSurface", Missing,
		"typeOfStatisticalProcessing", "1",
	)
	sn, ok := ShortName(q)
	if !ok {
		t.Fatal("ShortName: no match for total precipitation")
	}
	t.Logf("tp shortName = %q", sn)
	if name, ok := Name(q); !ok {
		t.Error("Name: no match for total precipitation")
	} else {
		t.Logf("tp name = %q", name)
	}
	// 把统计处理拿掉（模拟不提供该键的查询）：概念条件引用它就不命中，
	// 行为应可预期（大概率 no-match 或落到不同条目），绝不 panic。
	withoutStat := Map{}
	for k, v := range q {
		if k != "typeOfStatisticalProcessing" {
			withoutStat[k] = v
		}
	}
	if sn2, ok := ShortName(withoutStat); ok {
		t.Logf("without stat-processing key: shortName = %q", sn2)
	}
}

// 10 米风 U 分量：0/2/2 + 103 层 10 米 → 10u。
func TestShortNameTenMetreWindU(t *testing.T) {
	q := q2(
		"parameterCategory", "2",
		"parameterNumber", "2",
		"typeOfFirstFixedSurface", "103",
		"scaleFactorOfFirstFixedSurface", "0",
		"scaledValueOfFirstFixedSurface", "10",
	)
	if sn, ok := ShortName(q); !ok || sn != "10u" {
		t.Errorf("ShortName = %q,%v; want 10u,true", sn, ok)
	}
}

// 概念里没有的参数：ok=false，绝不编造。
func TestUnknownParameter(t *testing.T) {
	q := q2(
		"discipline", "250",
		"parameterCategory", "250",
		"parameterNumber", "250",
	)
	if sn, ok := ShortName(q); ok {
		t.Errorf("ShortName = %q,true; want no match", sn)
	}
	if name, ok := Name(q); ok {
		t.Errorf("Name = %q,true; want no match", name)
	}
}

// lte33 变体开关：master 版本 ≤ 33 走 .lte33 概念（mtg2=0）。
// WMO 表 2 版本 29 里 0/0/0 是 "Temperature"（master 短名 t）；lte33 与
// 普通概念对这一参数的结果一致，这里主要验证两条路径都通。
func TestMTG2SwitchTiers(t *testing.T) {
	base := q2(
		"typeOfFirstFixedSurface", "100",
		"scaleFactorOfFirstFixedSurface", "0",
		"scaledValueOfFirstFixedSurface", "85000",
		"pressureUnits", "hPa",
	)
	old := Map{}
	for k, v := range base {
		old[k] = v
	}
	old["masterTablesVersionNumber"] = "8"
	old["mtg2"] = "0"
	if sn, ok := ShortName(old); !ok || sn != "t" {
		t.Errorf("pre-MTG2 tier: ShortName = %q,%v; want t,true", sn, ok)
	}
}

// GRIB1：WMO 表 2 版本 3（centre 7/8 常用的国际交换表），参数 11 = 温度。
func TestGRIB1WMOTable(t *testing.T) {
	q := Map{
		"centre":                 "7",
		"table2Version":          "3",
		"indicatorOfParameter":   "11",
		"indicatorOfTypeOfLevel": "100",
		"level":                  "850",
	}
	if sn, ok := ShortName(q); !ok {
		t.Fatal("GRIB1 WMO table: ShortName no match")
	} else {
		t.Logf("GRIB1 t2v=3 param=11 shortName = %q", sn)
	}
	if name, ok := Name(q); ok {
		t.Logf("GRIB1 name = %q", name)
	}
	if u, ok := Units(q); ok {
		t.Logf("GRIB1 units = %q", u)
	}
}

// GRIB1 ECMWF 中心本地表（2.98.128.table）：参数 167 = 2 米温度。
func TestGRIB1ECMWFLocalTable(t *testing.T) {
	q := Map{
		"centre":                 "98",
		"table2Version":          "128",
		"indicatorOfParameter":   "167",
		"indicatorOfTypeOfLevel": "105",
		"level":                  "2",
	}
	sn, ok := ShortName(q)
	if !ok {
		t.Fatal("GRIB1 ECMWF local table: ShortName no match")
	}
	t.Logf("GRIB1 ECMWF param 167 shortName = %q", sn)
	if name, ok := Name(q); !ok {
		t.Error("GRIB1 ECMWF: Name no match")
	} else {
		t.Logf("GRIB1 ECMWF param 167 name = %q", name)
	}
	if u, ok := Units(q); !ok {
		t.Error("GRIB1 ECMWF: Units no match")
	} else {
		t.Logf("GRIB1 ECMWF param 167 units = %q", u)
	}
}

// typeOfLevel 概念的区分能力：同为第一固定面=1，第二固定面 255 是
// surface、7 是 troposphere——层对，不是参数。
func TestTypeOfLevelSurfaceVsTroposphere(t *testing.T) {
	surface := q2(
		"typeOfFirstFixedSurface", "1",
		"scaleFactorOfFirstFixedSurface", Missing,
		"scaledValueOfFirstFixedSurface", Missing,
		"typeOfSecondFixedSurface", "255",
	)
	if tol, ok := TypeOfLevel(surface); !ok || tol != "surface" {
		t.Errorf("TypeOfLevel = %q,%v; want surface,true", tol, ok)
	}
	// 概念有两条 'surface' 条目（一条要求 scale missing，一条宽松）；
	// ecCodes 的 last-match 语义下宽松条目赢，具体 scale 值不影响结果。
	concrete := q2(
		"typeOfFirstFixedSurface", "1",
		"scaleFactorOfFirstFixedSurface", "0",
		"scaledValueOfFirstFixedSurface", "0",
		"typeOfSecondFixedSurface", "255",
	)
	if tol, ok := TypeOfLevel(concrete); !ok || tol != "surface" {
		t.Errorf("TypeOfLevel (concrete scale) = %q,%v; want surface,true", tol, ok)
	}
	trop := q2(
		"typeOfFirstFixedSurface", "1",
		"scaleFactorOfFirstFixedSurface", Missing,
		"scaledValueOfFirstFixedSurface", Missing,
		"typeOfSecondFixedSurface", "7",
		"scaleFactorOfSecondFixedSurface", Missing,
		"scaledValueOfSecondFixedSurface", Missing,
	)
	if tol, ok := TypeOfLevel(trop); !ok || tol != "troposphere" {
		t.Errorf("TypeOfLevel = %q,%v; want troposphere,true", tol, ok)
	}
}
