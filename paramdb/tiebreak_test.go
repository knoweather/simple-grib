package paramdb

import (
	"strings"
	"testing"
)

// 生成数据里的层级编号（值与 cmd/paramgen/main.go 的 tierPrimary/Ecmf/Legacy
// 一致；公开 API 不暴露层级，这里按数值核对追加顺序）。
const (
	genTierPrimary = 1
	genTierEcmf    = 2
	genTierLegacy  = 3
)

// TestTieBreakDWD2mTemperature 覆盖 平手判定：同一层（tierPrimary）内
// 「中心本地概念」与「master 概念」条件数平手时的胜出方。
//
// 用例：DWD ICON 的 2 米气温消息——centre=78(edzw)、discipline=0、
// parameterCategory=0、parameterNumber=0、typeOfFirstFixedSurface=103、
// scaleFactorOfFirstFixedSurface=0、scaledValueOfFirstFixedSurface=2，
// masterTablesVersionNumber=19（≤ 33 → mtg2=0）。
//
// 逐条核对 paramdata/grib2/shortName.lte33.def 与
// paramdata/grib2/localConcepts/edzw/shortName.def 得到的条件数：
//
//	master '2t'   : mtg2=0 + discipline, parameterCategory, parameterNumber,
//	                typeOfFirstFixedSurface=103, scaledValueOfFirstFixedSurface=2,
//	                scaleFactorOfFirstFixedSurface=0                 → 7 条
//	edzw  'T_2M'  : centre=78 + discipline, parameterCategory, parameterNumber,
//	                typeOfFirstFixedSurface=103, scaleFactorOfFirstFixedSurface=0,
//	                scaledValueOfFirstFixedSurface=2                 → 7 条
//
// 两者条件全中且条件数相同 → 平手；平手取后者（paramdb.concept.lookup 的
// `cnt >= bestCnt`，与 ecCodes Concept.cc 的 concept_evaluate 一致）。
//
// ecCodes 2.47.0 oracle（本机 Python eccodes；可写键是 tablesVersion，
// masterTablesVersionNumber 会报 "Key/value not found"）：
// codes_grib_new_from_samples("GRIB2") 后设 centre=78、tablesVersion=19、
// discipline=0、parameterCategory=0、parameterNumber=0、
// typeOfFirstFixedSurface=103、scaleFactorOfFirstFixedSurface=0、
// scaledValueOfFirstFixedSurface=2，读回
//
//	shortName = 2t, name = "2 metre temperature", units = K, paramId = 167,
//	typeOfLevel = heightAboveGround
//
// （tablesVersion=34 同值；centre=98 的 ECMWF 2t 亦同。）
//
// 反向对照（同一键集，本机 eccodes 2.47.0 复跑）：把 ECC-806 的开关切到
// 「本地优先」——ECCODES_GRIB_PREFER_LOCAL_CONCEPTS=1——同一组键给出
//
//	shortName = T_2M, name = "2m Temperature", paramId = 500011, units = K
//
// 正是本库**修复前**的结果；默认（unset ⇒ 0）给出 2t。可见平手判定本身与
// ecCodes 一致，差别只在概念表顺序（dir1/dir2）——修复就是把这个顺序对齐。
//
// 依据 ecCodes ECC-806 的默认 preferLocalConcepts=0：grib2/section.1.def 里
// conceptsDir1 = conceptsLocalDirAll（本地/数据集）、conceptsDir2 =
// conceptsMasterDir（master），合并表序 dir1 → dir2，平手取后者 → **master 胜**。
// 修复前 cmd/paramgen 把 master 追加在最前，平手时反而本地 T_2M 胜，与 ecCodes
// 相反（平手判定：shortName T_2M vs 2t）。
func TestTieBreakDWD2mTemperature(t *testing.T) {
	// 键集对齐 paramdb/query.go 的 QueryFrom 为这样一条消息提供的键。
	q := Map{
		"centre":                         "78", // edzw
		"discipline":                     "0",
		"parameterCategory":              "0",
		"parameterNumber":                "0",
		"typeOfFirstFixedSurface":        "103", // 2 m above ground
		"scaleFactorOfFirstFixedSurface": "0",
		"scaledValueOfFirstFixedSurface": "2",
		// 单层消息的第二个固定面是 255（missing），QueryFrom 会给出这三个键；
		// typeOfLevel 概念的 heightAboveGround 条目（103 + 255）需要它们。
		"typeOfSecondFixedSurface":        "255",
		"scaleFactorOfSecondFixedSurface": Missing,
		"scaledValueOfSecondFixedSurface": Missing,
		"mtg2":                            "0", // masterTablesVersionNumber 19 ≤ 33
		"productDefinitionTemplateNumber": "0",
		"masterTablesVersionNumber":       "19",
		"localTablesVersion":              "0",
		"localTablesVersionNumber":        "0",
		"subCentre":                       "0",
		"productionStatusOfProcessedData": "0",
		"is_chemical":                     "0",
		"is_chemical_distfn":              "0",
		"is_chemical_srcsink":             "0",
		"is_aerosol":                      "0",
		"is_aerosol_optical":              "0",
	}
	if sn, ok := ShortName(q); !ok || sn != "2t" {
		t.Errorf("DWD ICON 2m 气温: ShortName = %q,%v; want 2t,true (ecCodes 2.47 oracle)", sn, ok)
	}
	if name, ok := Name(q); !ok || name != "2 metre temperature" {
		t.Errorf("DWD ICON 2m 气温: Name = %q,%v; want %q,true", name, ok, "2 metre temperature")
	}
	if u, ok := Units(q); !ok || u != "K" {
		t.Errorf("DWD ICON 2m 气温: Units = %q,%v; want K,true", u, ok)
	}
	if id, ok := ParamID(q); !ok || id != 167 {
		t.Errorf("DWD ICON 2m 气温: ParamID = %d,%v; want 167,true (ecCodes 2.47 oracle)", id, ok)
	}
	if tol, ok := TypeOfLevel(q); !ok || tol != "heightAboveGround" {
		t.Errorf("DWD ICON 2m 气温: TypeOfLevel = %q,%v; want heightAboveGround,true", tol, ok)
	}
}

// TestTieBreakLocalBeforeMasterOrder 直接核对生成数据的追加顺序：tierPrimary
// 内 centre-local 条目必须排在 master 条目之前（ecCodes ECC-806：
// dir1=本地、dir2=master），这样「平手取后者」才让 master 胜。
// 条件数相同（各 7 条）的 DWD 2m 气温对是最小可观测例子：若两者顺序颠倒，
// TestTieBreakDWD2mTemperature 会解析出 T_2M 而不是 2t。
func TestTieBreakLocalBeforeMasterOrder(t *testing.T) {
	local := map[string]string{
		"centre":                         "78",
		"discipline":                     "0",
		"parameterCategory":              "0",
		"parameterNumber":                "0",
		"typeOfFirstFixedSurface":        "103",
		"scaleFactorOfFirstFixedSurface": "0",
		"scaledValueOfFirstFixedSurface": "2",
	}
	master := map[string]string{
		"mtg2":                           "0",
		"discipline":                     "0",
		"parameterCategory":              "0",
		"parameterNumber":                "0",
		"typeOfFirstFixedSurface":        "103",
		"scaleFactorOfFirstFixedSurface": "0",
		"scaledValueOfFirstFixedSurface": "2",
	}
	iLocal, nLocal, tierLocal, foundLocal := findExactEntry(genShortNameGrib2, "T_2M", local)
	iMaster, nMaster, tierMaster, foundMaster := findExactEntry(genShortNameGrib2, "2t", master)
	if !foundLocal {
		t.Fatalf("生成数据里找不到条件集恰为 edzw T_2M 的条目（7 条条件）：%v", local)
	}
	if !foundMaster {
		t.Fatalf("生成数据里找不到条件集恰为 master 2t 的条目（mtg2=0 + 6 条条件）：%v", master)
	}
	if nLocal != 7 {
		t.Errorf("edzw T_2M 条件数 = %d, want 7", nLocal)
	}
	if nMaster != 7 {
		t.Errorf("master 2t 条件数 = %d, want 7", nMaster)
	}
	if tierLocal != genTierPrimary || tierMaster != genTierPrimary {
		t.Fatalf("两条目必须同在 tierPrimary 才需要平手决胜: T_2M tier=%d, 2t tier=%d", tierLocal, tierMaster)
	}
	if iLocal > iMaster {
		t.Errorf("tierPrimary 追加顺序错：edzw T_2M 下标 %d 在 master 2t 下标 %d 之后；"+
			"ecCodes 的 dir1(本地) → dir2(master) 要求本地在前，否则平手时本地胜（与 ecCodes 相反）",
			iLocal, iMaster)
	}
}

// findExactEntry 在概念表里找结果等于 result 且条件集**恰等于** want
// （键与取值都相同，个数也相同）的条目，返回其下标、条件数、层级与是否找到。
// 条件集恰好相等保证不会把带 centre 条件的本地条目当成 master 条目。
func findExactEntry(entries []genEntry, result string, want map[string]string) (idx, nConds, tier int, ok bool) {
	idx, nConds, tier = -1, 0, 0
	for i, e := range entries {
		if e.result != result {
			continue
		}
		conds := parsePackedConds(genConds[e.conds])
		if len(conds) != len(want) {
			continue
		}
		eq := true
		for k, v := range want {
			if cv, exists := conds[k]; !exists || cv != v {
				eq = false
				break
			}
		}
		if !eq {
			continue
		}
		if ok {
			// 条件集完全相同的重复条目：保留第一个仍然能定位，但值得暴露。
			// （平手语义取后者，这里只用于顺序/条件数核对。）
			continue
		}
		idx, nConds, tier, ok = i, len(conds), e.tier, true
	}
	return idx, nConds, tier, ok
}

// parsePackedConds 解开 "k=v;k=v" 打包串（与 paramdb.build 同一格式）。
func parsePackedConds(packed string) map[string]string {
	out := map[string]string{}
	for _, frag := range strings.Split(packed, ";") {
		if frag == "" {
			continue
		}
		if eq := strings.Index(frag, "="); eq >= 0 {
			out[frag[:eq]] = frag[eq+1:]
		}
	}
	return out
}
