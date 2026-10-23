package paramdb

import "strconv"

// 本文件实现 ecCodes 的 MTG2Switch —— 参数概念文件（shortName/name/paramId/
// units）在 pre-MTG2 与 post-MTG2 两套定义之间切换的开关。
//
// ecCodes 的机制（definitions 原文，2.50 版）：
//
//	grib2/section.1.def:  constant tablesVersionMTG2Switch = 33;
//	grib2/parameters.def: meta shortNameFilename g2_param_concept_filename(
//	                          "shortName", MTG2Switch, tablesVersionMTG2Switch);
//	  注释原文："Pre-MTG2 ( MTG2Switch=0 ): static def files
//	             [concept].lte[tablesVersionMTG2Switch].def;
//	             Else ( MTG2Switch!=0 ): default def files [concept].def."
//	grib2/section.4.def:  MTG2SwitchViaTablesVersion（顶层 + 中心/数据集目录）
//	   命中 1 → 走 tablesVersion 规则；不命中（默认 false）→ MTG2SwitchDefault=1
//	   （"By default centres will use post MTG2 parameter concepts (= 1) to retain
//	     the status quo"）
//	   tablesVersion 规则：tv <= 33 → 0；tv > 33 且 class=mc/cr → 2；否则 1
//	MTG2Switch 概念（中心/数据集目录 + 顶层）在默认值之上做覆盖。
//
// 生成的条目表（genMTG2Via / genMTG2Switch）来自 paramdata/grib2 里 vendored 的
// MTG2SwitchViaTablesVersionConcept.def 与 MTG2SwitchConcept.def（顶层 + 各目录），
// 由 cmd/paramgen 生成，见 main.go 的 MTG2 块。
//
// 同一条 DWD ICON 数据在不同 subCentre / 表版本下的取值（ecCodes 2.50 的结果，
// paramdb/mtg2_test.go 逐条钉住）：
//
//	subCentre=255 tablesVersion=19 → MTG2Switch=1 → max_2t / 237167
//	subCentre=98  tablesVersion=19 → MTG2Switch=0 → mx2t  / 201
//	subCentre=98  tablesVersion=33 → MTG2Switch=0 → mx2t  / 201
//	subCentre=98  tablesVersion=34 → MTG2Switch=1 → max_2t / 237167
//
// subCentre 从 255 变成 98 会翻转结果，正对应顶层
// `1 = {subCentre=98;}` / `0 = {subCentre=98; marsClass=50;}` 两条规则。
const mtg2SwitchTablesVersion = 33

// MTG2Switch 的三个取值（见上）。
const (
	MTG2Pre      = 0 // pre-MTG2 编码：概念文件用 <concept>.lte33.def
	MTG2Post     = 1 // post-MTG2：用 <concept>.def
	MTG2PostChem = 2 // post-MTG2 + chemId 拆分：同样用 <concept>.def
)

// mtg2Concepts 由生成表构建，init 里初始化（genMTG2Via / genMTG2Switch 在
// concepts_gen.go）。
var (
	mtg2ViaConcept    tiers
	mtg2SwitchConcept tiers
)

func init() {
	mtg2ViaConcept = buildTiers(genMTG2Via)
	mtg2SwitchConcept = buildTiers(genMTG2Switch)
}

// MTG2Switch 返回 ecCodes 的 MTG2Switch（0/1/2）。q 需含 tablesVersion /
// masterTablesVersionNumber、subCentre、centre、marsClass（有则给）等键。
func MTG2Switch(q Map) int {
	// 默认 = status quo = post-MTG2（section.4.def 注释）。
	sw := MTG2Post
	// MTG2SwitchViaTablesVersion 概念：命中 1 才走 tablesVersion 规则。
	if v, ok := mtg2ViaConcept.lookup(q); ok && v == "1" {
		tv, _ := atoi(q["tablesVersion"])
		switch {
		case tv <= mtg2SwitchTablesVersion:
			sw = MTG2Pre
		case isMarsClassMCOrCR(q):
			sw = MTG2PostChem
		default:
			sw = MTG2Post
		}
	}
	// MTG2Switch 概念覆盖默认值；条目取值不是 0/1/2（如顶层占位 -1）时保持默认。
	if v, ok := mtg2SwitchConcept.lookup(q); ok {
		if n, err := strconv.Atoi(v); err == nil && (n == 0 || n == 1 || n == 2) {
			sw = n
		}
	}
	return sw
}

// isMarsClassMCOrCR 判断 section.4.def 注释里的 class=mc/cr：forecast（mc=19）
// 与 reanalysis-comparable cr=36（取值见 localConcepts/ecmf/MTG2SwitchConcept.def
// 里 MC/CR 分节的 marsClass）。
func isMarsClassMCOrCR(q Map) bool {
	mc, _ := atoi(q["marsClass"])
	return mc == 19 || mc == 36
}

// conceptMtg2Key 把 MTG2Switch 折算成概念表里的条件值：0 → "0"（.lte33 概念），
// 1 与 2 都用普通概念 → "1"。
func conceptMtg2Key(sw int) string {
	if sw == MTG2Pre {
		return "0"
	}
	return "1"
}
