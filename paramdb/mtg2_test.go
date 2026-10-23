package paramdb

import "testing"

// 本文件钉住 ecCodes 的 MTG2Switch 派生（见 mtg2.go）。期望值取自 ecCodes 2.50
// 对同一条 DWD ICON 消息在不同 subCentre / 表版本 / 数据集门控下的结果；2.47 相同。
//
// 背景：ecCodes 用 MTG2Switch 选 <concept>.lte33.def 还是 <concept>.def。
// 只看 masterTablesVersion ≤ 33 不够：DWD ICON 是 tv=19 但 subCentre=255，
// subCentre 规则不命中，ecCodes 用的是 post-MTG2 的 max_2t。

// tmaxConds 是「2 米最高气温」的概念条件集（无 mtg2/中心/版本键）。
func tmaxConds() Map {
	return Map{
		"discipline":                      "0",
		"parameterCategory":               "0",
		"parameterNumber":                 "0",
		"typeOfFirstFixedSurface":         "103",
		"scaledValueOfFirstFixedSurface":  "2",
		"scaleFactorOfFirstFixedSurface":  "0",
		"typeOfStatisticalProcessing":     "2",
		"productDefinitionTemplateNumber": "8",
		"typeOfSecondFixedSurface":        "255",
		"localTablesVersionNumber":        "1",
		"masterTablesVersionNumber":       "29",
	}
}

// tminConds 是「2 米最低气温」（统计处理 = 3）。
func tminConds() Map {
	q := tmaxConds()
	q["typeOfStatisticalProcessing"] = "3"
	return q
}

// gustConds 是「10 米最大阵风」（parameterCategory=2, parameterNumber=22）。
func gustConds() Map {
	q := tmaxConds()
	q["parameterCategory"] = "2"
	q["parameterNumber"] = "22"
	q["scaledValueOfFirstFixedSurface"] = "10"
	return q
}

// wantKey 把 MTG2Switch 折算成概念条件下的 mtg2 取值。
func wantKey(sw int) string {
	if sw == MTG2Pre {
		return "0"
	}
	return "1"
}

// withPspd 设置 productionStatusOfProcessedData（数据集门控键）。
func withPspd(q Map, v string) Map {
	q["productionStatusOfProcessedData"] = v
	return withMtg2(q)
}

// withMtg2 按 ecCodes 规则算出 mtg2 键（QueryFrom 的同一步骤）。
func withMtg2(q Map) Map {
	q["mtg2"] = conceptMtg2Key(MTG2Switch(q))
	return q
}

func TestMTG2SwitchOracleCases(t *testing.T) {
	cases := []struct {
		name       string
		centre     string
		subCentre  string
		tv         string
		marsClass  string
		pspd       string
		wantSwitch int
	}{
		// subCentre 规则：只有 subCentre=98 才走 tablesVersion 通道。
		{"edzw subCentre=255 tv=19（DWD ICON 实况）", "78", "255", "19", "", "", MTG2Post},
		{"edzw subCentre=98 tv=19", "78", "98", "19", "", "", MTG2Pre},
		{"edzw subCentre=98 tv=33", "78", "98", "33", "", "", MTG2Pre},
		{"edzw subCentre=98 tv=34", "78", "98", "34", "", "", MTG2Post},
		{"edzw subCentre=0 tv=29", "74", "0", "29", "", "", MTG2Post},
		// ecmf（中心 98）目录的 MTG2SwitchViaTablesVersion 文件是 1 = {true=1;}：
		// 任何 subCentre 都命中 tablesVersion 通道。
		{"ecmf subCentre=0 tv=33", "98", "0", "33", "", "", MTG2Pre},
		{"ecmf subCentre=255 tv=33", "98", "255", "33", "", "", MTG2Pre},
		{"ecmf subCentre=0 tv=34", "98", "0", "34", "", "", MTG2Post},
		// class=mc/cr 且 tv>33 → 2（chemId 拆分），概念上仍用普通文件。
		{"ecmf tv=34 class=19 (mc)", "98", "0", "34", "19", "", MTG2PostChem},
		{"ecmf tv=34 class=36 (cr)", "98", "0", "36", "36", "", MTG2PostChem},
		// 顶层例外：subCentre=98 且 marsClass=50（CERISE）→ via=0；再由 cerise
		// 目录的 MTG2SwitchConcept.def（0 = {true=1;}）覆盖成 pre-MTG2。
		{"cerise subCentre=98 class=50", "98", "98", "11", "50", "", MTG2Pre},
		// 数据集目录的 MTG2SwitchConcept.def 覆盖：era→0、era6→1。
		{"era class=22 tv=11", "98", "0", "11", "22", "", MTG2Pre},
		{"era6 class=40 tv=11", "98", "0", "11", "40", "", MTG2Post},
		// 数据集门控：tigge/s2s/uerra/destine 目录的 MTG2SwitchViaTablesVersionConcept.def
		// 是 1 = {true=1;}，命中即 via=1 → tv ≤ 33 → pre-MTG2。ecCodes 2.50 对
		// tv=19、只改 productionStatusOfProcessedData 的 DWD 消息给 mx2t/201。
		{"tigge pspd=4 tv=19", "78", "255", "19", "", "4", MTG2Pre},
		{"s2s pspd=6 tv=19", "78", "255", "19", "", "6", MTG2Pre},
		{"uerra pspd=8 tv=19", "78", "255", "19", "", "8", MTG2Pre},
		{"destine pspd=12 tv=19", "78", "255", "19", "", "12", MTG2Pre},
		{"非数据集 pspd=1 tv=19", "78", "255", "19", "", "1", MTG2Post},
		{"pspd=255 tv=19", "78", "255", "19", "", "255", MTG2Post},
	}
	for _, c := range cases {
		func() {
			q := Map{"centre": c.centre, "subCentre": c.subCentre, "tablesVersion": c.tv}
			if c.marsClass != "" {
				q["marsClass"] = c.marsClass
			}
			if c.pspd != "" {
				q["productionStatusOfProcessedData"] = c.pspd
			}
			if got := MTG2Switch(q); got != c.wantSwitch {
				t.Errorf("%s：MTG2Switch = %d，想要 %d", c.name, got, c.wantSwitch)
			}
			if got := conceptMtg2Key(MTG2Switch(q)); got != wantKey(c.wantSwitch) {
				t.Errorf("%s：概念 mtg2 键 = %q，想要 %q", c.name, got, wantKey(c.wantSwitch))
			}
		}()
	}
}

// TestMTG2AffectsParameterNames 是问题 3 定案的三个真实报文：短名 / paramId
// 必须与 ecCodes 一致（max_2t/237167、min_2t/238167、max_i10fg/237318）。
func TestMTG2AffectsParameterNames(t *testing.T) {
	newMap := func(conds Map, centre, subCentre, tv string) Map {
		q := conds
		q["centre"] = centre
		q["subCentre"] = subCentre
		q["tablesVersion"] = tv
		q["masterTablesVersionNumber"] = tv
		return withMtg2(q)
	}
	cases := []struct {
		name string
		q    Map
		want int // paramId
	}{
		{"DWD ICON TMAX_2M（subCentre=255, tv=19）", newMap(tmaxConds(), "78", "255", "19"), 237167},
		{"DWD ICON TMIN_2M（subCentre=255, tv=19）", newMap(tminConds(), "78", "255", "19"), 238167},
		{"10 米最大阵风（centre=74, tv=29）", newMap(gustConds(), "74", "0", "29"), 237318},
		{"tigge pspd=4 tv=19（数据集门控）", withPspd(newMap(tmaxConds(), "78", "255", "19"), "4"), 201},
		{"subCentre=98 tv=19 → pre-MTG2 概念", newMap(tmaxConds(), "78", "98", "19"), 201},
		{"subCentre=98 tv=19 → pre-MTG2 概念（最低温）", newMap(tminConds(), "78", "98", "19"), 202},
	}
	wantShortNames := []string{"max_2t", "min_2t", "max_i10fg", "mx2t", "mx2t", "mn2t"}
	for i, c := range cases {
		got, ok := ShortName(c.q)
		if !ok || got != wantShortNames[i] {
			t.Errorf("%s：shortName = %q,%v，想要 %q", c.name, got, ok, wantShortNames[i])
		}
		if pid, ok := ParamID(c.q); !ok || pid != c.want {
			t.Errorf("%s：paramId = %d,%v，想要 %d", c.name, pid, ok, c.want)
		}
	}
}

// TestMTG2TablesVersionAlias：ecCodes 的 `tablesVersion` 与
// `masterTablesVersionNumber` 是别名（section.1.def 的 alias），两个键都必须能
// 驱动开关，否则概念条目里的 tablesVersion 条件永不命中。
func TestMTG2TablesVersionAlias(t *testing.T) {
	for _, key := range []string{"tablesVersion", "masterTablesVersionNumber"} {
		q := Map{"centre": "78", "subCentre": "98", key: "19", "masterTablesVersionNumber": "19"}
		if key == "masterTablesVersionNumber" {
			delete(q, "tablesVersion")
		}
		if got := MTG2Switch(q); got != MTG2Pre {
			t.Errorf("只有 %s 时 MTG2Switch = %d，想要 %d", key, got, MTG2Pre)
		}
	}
}
