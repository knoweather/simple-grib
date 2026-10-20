// 命令 paramgen 从 paramdata/（vendored、版本钉死的 ecCodes definitions 子集）
// 生成 paramdb 包的概念表 Go 数据。
//
// ecCodes 用 "concept" 机制回答 shortName / name / paramId / units /
// typeOfLevel：每个概念是一张有序条目表，每条 = 一组条件（键=值）+ 结果
// token；层内取"条件全中且条件数最多"的条目（平手取后者）。条件键是消息的其它键
// （discipline/parameterCategory/parameterNumber/各级固定面/统计处理类型…），
// 因此 2 米温度（0/0/0 + typeOfFirstFixedSurface=103, scaledValue=2）与
// 等压面温度（同参数号 + 100 层）能得到不同的 shortName（2t vs t）——
// 这正是纯参数号映射表做不到的地方。
//
// 输入（paramdata/，来源 ecCodes 2.50.0，见 PROVENANCE.txt）：
//   - grib2/{shortName,name,paramId,units}{,.lte33,.legacy}.def
//     四个文件由 ecCodes 的同一脚本并行生成：条件序列完全一致，仅结果不同。
//     生成器按 lockstep 解析（数量与条件序列逐一核对，不一致即报错），
//     因此一份条件可以带四个结果。
//     .lte33 变体只用于 masterTablesVersionNumber ≤ 33 的消息（MTG2 切换，
//     阈值 33，见 grib2/section.1.def 的 tablesVersionMTG2Switch）；普通
//     .def 用于 ≥ 34。生成器给条目打上 mtg2=0/1 条件，查询方提供该键。
//   - grib2/typeOfLevelConcept.def（typeOfLevel：面类型 → 名字）
//   - grib2/localConcepts/<centre>/…（各中心本地概念，ecmf 优先）
//   - grib1/{shortName,name,paramId,units}.def（WMO 表 2 版本 1-3）
//   - grib1/typeOfLevel.def、grib1/2.<centre>.<version>.table
//     （中心本地 GRIB1 参数表：code shortname "name (unit)"，覆盖 ECMWF 128 等）
//
// 输出（paramdb/）：concepts_gen.go —— 有序条目切片，层内 best-match（条件
// 最多者胜，平手取后者）。查找顺序（模拟 ecCodes 的概念链，见
// grib2/parameters.def）：master → 其它中心（字典序）→ ecmf → legacy（兜底）。
//
// 与 specgen 一样「失败即报错」：解析不了、lockstep 破坏、值里出现分隔符，
// 都带着文件+行号中止。
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// missingToken 与 paramdb.Missing 必须逐字节一致：条件 missing() 归一成它，
// 查询方把"消息里该键存在但取值缺失"也表示成它。
const missingToken = "\x00missing"

// 概念链层级（ecCodes grib2/parameters.def 的概念链）：
//
//	tierPrimary: master + 消息中心本地概念（同层拼接，last-match 下 master
//	             在后覆盖本地；本地补 master 没有的码段）
//	tierEcmf:    shortNameECMF 兜底链（conceptsLocalDirECMF 硬编码 ecmf，
//	             对非 ECMWF 消息也会查）
//	tierLegacy:  最早的 legacy 概念（master 在前、ecmf 在后）
//
// 查询语义：tier 间"第一个有命中的层胜出"，层内"最后一个匹配的条目胜出"。
const (
	tierPrimary = 1
	tierEcmf    = 2
	tierLegacy  = 3
)

// GRIB1 的概念链（grib1/section.1.def 的 paramId/name/units/shortName 定义）：
//
//	tierG1Local  消息中心自己的 grib1/localConcepts/<centre>
//	tierG1ECMF   grib1/localConcepts/ecmf（对所有消息生效的兜底）
//	tierG1Master WMO 表 2 版本 1-3（grib1/*.def）
//	tierG1Table  2.<centre>.<version>.table（显示名兜底；ecCodes 里不是概念，
//	             用来覆盖没有 localConcepts 文件的中心）
const (
	tierG1Local  = 1
	tierG1ECMF   = 2
	tierG1Master = 3
	tierG1Table  = 4
)

// listCentres 列出 localConcepts 下至少含一个概念文件的中心目录，字典序。
func listCentres(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fatal("%v", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "shortName.def")); err == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func main() {
	dir := flag.String("dir", "paramdata", "directory with vendored ecCodes definitions")
	out := flag.String("out", "paramdb", "output directory for generated Go file")
	checkOnly := flag.Bool("check", false, "verify the generated file is in sync; do not write")
	flag.Parse()

	prov := readProvenance(filepath.Join(*dir, "PROVENANCE.txt"))

	// 每个概念一条独立有序表；层级（master → 消息中心本地 → ecmf → legacy）
	// 由追加顺序编码——先匹配先赢，平手取后者（last-match）。为了在平手时让更特
	// 殊的条目胜出，必须在同级内按“条件最少 → 最多”的顺序追加：master 最先、
	// 数据集（带额外生产状态/世代条件）居中、centre-local 和 ecmf 最后。
	pool := newCondPool()
	var snG2, nameG2, pidG2, unitG2 []outEntry

	// 中心助记符 → 中心号（CCT-1）：本地概念目录按消息中心圈定，
	// 这是 ecCodes 的 conceptsLocalDirAll="grib2/localConcepts/[centre:s]"。
	centreOf := parseC1Table(filepath.Join(*dir, "common", "c-1.table"))

	// 层级顺序（模拟 ecCodes 概念链，见 grib2/parameters.def）：
	// 1) master（默认优先）
	// 2) 消息中心自己的本地概念（conceptsLocalDirAll 按中心圈定）
	// 3) ecmf 本地（shortNameECMF 兜底链）
	// 4) legacy（最底层兜底）
	// 数据集伪中心（tigge/s2s/uerra/destine …）通过 productionStatusOfProcessedData
	// 激活，挂在主层末尾（centre-local 之后），平手时胜出——对齐 ecCodes 的
	// datasetForLocal 重映射（数据集目录替换中心本地目录）。
	centres := listCentres(filepath.Join(*dir, "grib2", "localConcepts"))
	tiers := []tierSource{{centre: ""}} // master
	for _, c := range centres {
		if _, ok := centreOf[c]; ok && c != "ecmf" {
			tiers = append(tiers, tierSource{centre: c})
		}
	}
	tiers = append(tiers, tierSource{centre: "ecmf"}) // 兜底链里的 ECMWF 本地

	// 伪中心数据集目录定义（来自 section.4.def 的 concept datasetForLocal）。
	// 分两类：
	//   1) 仅靠 productionStatusOfProcessedData 激活（tigge/s2s/uerra/destine …
	//      destinegen1/lcgcr/wpmip）：挂在主层末尾（centre-local 之后），条件更
	//      特异，平手时胜出。
	//   2) 靠 marsClass 激活（era/era6/cerise/hydro/hydro_legacy/fire/nextgems …）：
	//      本实现尚未派生 mars 键，这些目录暂不接入（见总结文档"仍缺"清单）。
	type pseudoDataset struct {
		dir string
		sts []int // productionStatusOfProcessedData 的激活值
		mcs []int // marsClass 的激活值（整数 code）
		gen int   // 额外 generation 条件；-1 = 无
	}
	pseudoDatasets := []pseudoDataset{
		{dir: "tigge", sts: []int{4, 5}, gen: -1},
		{dir: "s2s", sts: []int{6, 7}, gen: -1},
		{dir: "uerra", sts: []int{8, 9, 10, 11}, gen: -1},
		{dir: "destine", sts: []int{12, 13}, gen: -1},
		{dir: "destinegen1", sts: []int{12, 13}, gen: 1},
		{dir: "lcgcr", sts: []int{14, 15}, gen: -1},
		{dir: "wpmip", sts: []int{16, 17}, gen: -1},
		// marsClass-gated：条件值取自 grib2/section.4.def 的 datasetForLocal。
		// marsClass 是 codetable，那里的条件写整数 code（`marsClass=50;` 等），
		// Concept.cc 按字面量 native_type 走 grib_get_long，故这里也用整数。
		{dir: "cerise", mcs: []int{50}, gen: -1},
		{dir: "era6", mcs: []int{40}, gen: -1},
		{dir: "hydro_legacy", mcs: []int{35, 38}, gen: -1},
		{dir: "hydro", mcs: []int{42, 43, 48, 49}, gen: -1},
		{dir: "fire", mcs: []int{39}, gen: -1},
		{dir: "era", mcs: []int{3, 5, 14, 21, 22, 23, 24, 30, 33}, gen: -1},
		{dir: "nextgems", mcs: []int{53}, gen: -1},
	}
	pseudoHandled := map[string]bool{} // 已处理（含 vendored 子集里为空的）目录
	// 伪中心条目先缓冲，待 tiers 循环（master + centre-local + ecmf）追加完后再
	// 拼进主层——这样它们在同层内排在 centre-local 之后，平手时胜出，对齐
	// ecCodes 的 datasetForLocal 重映射（数据集目录替换中心本地目录）。
	var pSnG2, pNameG2, pPidG2, pUnitG2 []outEntry

	// 为每条激活条件生成一组概念条目，先缓冲。
	for _, pd := range pseudoDatasets {
		base := filepath.Join(*dir, "grib2", "localConcepts", pd.dir)
		// 目录可能只有 MTG2 变体：cerise/hydro_legacy 只有 .lte33，era 只有
		// .legacy+.lte33。只要存在任一 shortName 变体就算有概念文件。
		hasConcepts := false
		for _, sfx := range []string{"", ".lte33", ".legacy"} {
			if _, statErr := os.Stat(filepath.Join(base, "shortName"+sfx+".def")); statErr == nil {
				hasConcepts = true
				break
			}
		}
		if !hasConcepts {
			// vendored 子集里没有概念文件（destinegen1/lcgcr/wpmip/hydro/fire）。
			fmt.Fprintf(os.Stderr, "paramgen: note: localConcepts/%s has no concept files in vendored subset, skipped\n", pd.dir)
			pseudoHandled[pd.dir] = true
			continue
		}
		pseudoHandled[pd.dir] = true
		// 每条激活值一个条件前缀。sts（按 productionStatus）与 mcs（按 marsClass）
		// 互斥，列表里不会同时给。
		type activation struct {
			st, mc int
			bySt   bool
		}
		var acts []activation
		for _, st := range pd.sts {
			acts = append(acts, activation{st: st, bySt: true})
		}
		for _, mc := range pd.mcs {
			acts = append(acts, activation{mc: mc})
		}
		for _, a := range acts {
			// 伪中心条目：激活条件 + 可选 generation 条件。
			var condPrefix string
			if pd.gen >= 0 {
				condPrefix = fmt.Sprintf("generation=%d;", pd.gen)
			}
			if a.bySt {
				condPrefix += fmt.Sprintf("productionStatusOfProcessedData=%d;", a.st)
			} else {
				condPrefix += fmt.Sprintf("marsClass=%d;", a.mc)
			}
			// MTG2 变体与中心目录同规则（见上）：有 .lte33 变体 → 普通条目带
			// mtg2=1、lte33 条目带 mtg2=0；没有 .lte33 → 普通条目不带 mtg2 条件。
			mtg2Plain := "mtg2=1"
			if _, lteErr := os.Stat(filepath.Join(base, "shortName.lte33.def")); lteErr != nil {
				mtg2Plain = ""
			}
			for _, v := range []struct{ suffix, mtg2 string }{{"", mtg2Plain}, {".lte33", "mtg2=0"}, {".legacy", ""}} {
				suffix, mtg2 := v.suffix, v.mtg2
				prep := condPrefix
				if mtg2 != "" {
					prep += mtg2 + ";"
				}
				entryTier := tierPrimary // 伪中心始终在主层
				if suffix == ".legacy" {
					entryTier = tierLegacy
				}
				baseName := "shortName" + suffix + ".def"
				snList, err := parseDefFile(filepath.Join(base, baseName))
				if err != nil {
					// 伪中心目录可能只有部分 MTG2 变体（cerise/hydro_legacy 只有
					// .lte33，era 只有 .legacy+.lte33）：缺失的变体跳过。
					if os.IsNotExist(err) {
						continue
					}
					fatal("%v", err)
				}
				nameList := mustParseOptional(filepath.Join(base, "name"+suffix+".def"))
				pidList := mustParseOptional(filepath.Join(base, "paramId"+suffix+".def"))
				unitList := mustParseOptional(filepath.Join(base, "units"+suffix+".def"))
				pSnG2 = append(pSnG2, pool.emit(snList, entryTier, "", prep)...)
				pNameG2 = append(pNameG2, pool.emit(nameList, entryTier, "", prep)...)
				pPidG2 = append(pPidG2, pool.emit(pidList, entryTier, "", prep)...)
				pUnitG2 = append(pUnitG2, pool.emit(unitList, entryTier, "", prep)...)
			}
		}
	}

	// 其余无 CCT-1 中心号的目录（lfpw1 等）：目录名既不是消息中心号，也不在
	// datasetForLocal 里，无法按中心或数据集圈定，故不接入。
	for _, c := range centres {
		if _, ok := centreOf[c]; !ok && !pseudoHandled[c] {
			fmt.Fprintf(os.Stderr, "paramgen: note: localConcepts/%s skipped (no CCT-1 centre number and not a datasetForLocal entry)\n", c)
		}
	}

	for _, t := range tiers {
		base := filepath.Join(*dir, "grib2")
		if t.centre != "" {
			base = filepath.Join(base, "localConcepts", t.centre)
		}
		// 本地概念按消息中心圈定：给条目补 centre 条件。一个助记符可能有多个
		// 中心号（lfpw = 84/85），ecCodes 按助记符选目录，故每个号各生成一套。
		var centreConds []string
		if t.centre != "" {
			ns, ok := centreOf[t.centre]
			if !ok {
				continue
			}
			for _, n := range ns {
				centreConds = append(centreConds, "centre="+strconv.Itoa(n)+";")
			}
		} else {
			centreConds = append(centreConds, "") // 主层：不带 centre 条件
		}
		tier := tierPrimary
		if t.centre == "ecmf" {
			tier = tierEcmf // ecmf 目录在兜底链里再出现一次
		}
		// MTG2 的 .lte33/普通文件之分只存在于 master（以及部分中心目录）。
		// ecCodes 对没有 .lte33 变体的中心（edzw、eswi、cnmc、ekmi、efkl…）
		// 在所有表版本下都用同一个文件，所以这些普通条目不能带 mtg2 条件，
		// 否则 tablesVersion ≤ 33 的真实消息（如 DWD ICON）会整层落空。
		mtg2Plain := "mtg2=1"
		if t.centre != "" {
			if _, err := os.Stat(filepath.Join(base, "shortName.lte33.def")); err != nil {
				mtg2Plain = ""
			}
		}
		for _, v := range []struct{ suffix, mtg2 string }{{"", mtg2Plain}, {".lte33", "mtg2=0"}, {".legacy", ""}} {
			variant, mtg2 := v.suffix, v.mtg2
			entryTier := tier
			if variant == ".legacy" {
				entryTier = tierLegacy // legacy 概念是独立的兜底层
			}
			baseName := "shortName" + variant + ".def"
			list, err := parseDefFile(filepath.Join(base, baseName))
			if err != nil {
				if os.IsNotExist(err) && variant != "" {
					continue // 该中心没有这一变体，正常
				}
				fatal("%v", err)
			}
			nameList := mustParseOptional(filepath.Join(base, "name"+variant+".def"))
			pidList := mustParseOptional(filepath.Join(base, "paramId"+variant+".def"))
			unitList := mustParseOptional(filepath.Join(base, "units"+variant+".def"))
			for _, centreCond := range centreConds {
				snG2 = append(snG2, pool.emit(list, entryTier, mtg2, centreCond)...)
				nameG2 = append(nameG2, pool.emit(nameList, entryTier, mtg2, centreCond)...)
				pidG2 = append(pidG2, pool.emit(pidList, entryTier, mtg2, centreCond)...)
				unitG2 = append(unitG2, pool.emit(unitList, entryTier, mtg2, centreCond)...)
			}
		}
	}

	// 把缓冲的伪中心条目拼到主层末尾（见上：平手时胜过 centre-local）。
	snG2 = append(snG2, pSnG2...)
	nameG2 = append(nameG2, pNameG2...)
	pidG2 = append(pidG2, pPidG2...)
	unitG2 = append(unitG2, pUnitG2...)

	var snG1, nameG1, pidG1, unitG1 []outEntry
	{
		base := filepath.Join(*dir, "grib1")
		list, err := parseDefFile(filepath.Join(base, "shortName.def"))
		if err != nil {
			fatal("%v", err)
		}
		snG1 = append(snG1, pool.emit(list, tierG1Master, "", "")...)
		nameG1 = append(nameG1, pool.emit(mustParseOptional(filepath.Join(base, "name.def")), tierG1Master, "", "")...)
		pidG1 = append(pidG1, pool.emit(mustParseOptional(filepath.Join(base, "paramId.def")), tierG1Master, "", "")...)
		unitG1 = append(unitG1, pool.emit(mustParseOptional(filepath.Join(base, "units.def")), tierG1Master, "", "")...)
	}
	// 中心本地 GRIB1 概念（grib1/localConcepts/<centre>）：消息中心那一层
	// 按 centre 圈定；ecmf 目录额外作为不受中心限制的兜底层（ecCodes 的
	// conceptsLocalDirECMF）。这层补上了中心 .table 没有的 paramId，并让
	// name/units 与 ecCodes 的 name.def/units.def 逐字一致。
	if entries, err := os.ReadDir(filepath.Join(*dir, "grib1", "localConcepts")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			tier, centreConds := tierG1Local, []string{""}
			if name == "ecmf" {
				tier = tierG1ECMF
			} else if ns, ok := centreOf[name]; ok {
				centreConds = nil
				for _, n := range ns {
					centreConds = append(centreConds, "centre="+strconv.Itoa(n)+";")
				}
			} else {
				fmt.Fprintf(os.Stderr, "paramgen: note: grib1/localConcepts/%s skipped (no CCT-1 centre number)\n", name)
				continue
			}
			base := filepath.Join(*dir, "grib1", "localConcepts", name)
			snList := mustParseOptional(filepath.Join(base, "shortName.def"))
			nameList := mustParseOptional(filepath.Join(base, "name.def"))
			pidList := mustParseOptional(filepath.Join(base, "paramId.def"))
			unitList := mustParseOptional(filepath.Join(base, "units.def"))
			for _, centreCond := range centreConds {
				snG1 = append(snG1, pool.emit(snList, tier, "", centreCond)...)
				nameG1 = append(nameG1, pool.emit(nameList, tier, "", centreCond)...)
				pidG1 = append(pidG1, pool.emit(pidList, tier, "", centreCond)...)
				unitG1 = append(unitG1, pool.emit(unitList, tier, "", centreCond)...)
			}
		}
	}
	// 中心本地 GRIB1 参数表（code shortname "name (unit)"）作为最后一层兜底：
	// 它们没有 paramId，name 是显示名，只用于没有 localConcepts 文件的中心。
	g1Tables := parseGrib1CentreTables(filepath.Join(*dir, "grib1"))
	for _, e := range g1Tables {
		snG1 = append(snG1, outEntry{tier: tierG1Table, conds: pool.intern(e.conds), result: e.sn})
		nameG1 = append(nameG1, outEntry{tier: tierG1Table, conds: pool.intern(e.conds), result: e.name})
		unitG1 = append(unitG1, outEntry{tier: tierG1Table, conds: pool.intern(e.conds), result: e.unit})
	}

	tolG2 := pool.single(filepath.Join(*dir, "grib2", "typeOfLevelConcept.def"), tierPrimary)
	tolG1 := pool.single(filepath.Join(*dir, "grib1", "typeOfLevel.def"), tierPrimary)

	// MARS 标签表（class/type/stream）——与概念表分开成独立产物 mars_gen.go。
	marsClass, err := parseMarsTable(filepath.Join(*dir, "mars", "class.table"))
	if err != nil {
		fatal("%v", err)
	}
	marsType, err := parseMarsTable(filepath.Join(*dir, "mars", "type.table"))
	if err != nil {
		fatal("%v", err)
	}
	marsStream, err := parseMarsTable(filepath.Join(*dir, "mars", "stream.table"))
	if err != nil {
		fatal("%v", err)
	}

	// MARS 标签门控表：哪些本地定义会提供 marsClass/marsType/marsStream/
	// experimentVersionNumber。读消息的一方（grib、grib1 包）拿它决定要不要解析
	// 这几个键，判据与 ecCodes 的模板引用链一致。
	g1Mars, g2Mars, err := parseMarsGate(*dir)
	if err != nil {
		fatal("%v", err)
	}

	// 生成物分散在两个包里：概念表与 MARS 缩写表在 paramdb；两张 MARS 门控表
	// 各自落在读消息的包里（根目录是 package grib，grib1/ 是 package grib1）。
	// libRoot 是从 -out 推出的仓库根（默认 -out paramdb → "."）。
	libRoot := filepath.Dir(*out)
	outputs := []struct{ dir, name, src string }{
		{*out, "concepts_gen.go", render(prov, pool, snG2, nameG2, pidG2, unitG2, snG1, nameG1, pidG1, unitG1, tolG2, tolG1)},
		{*out, "mars_gen.go", renderMars(prov, marsClass, marsType, marsStream)},
		{filepath.Join(libRoot, "grib1"), "mars_gen.go", renderMarsGateGRIB1(prov, g1Mars)},
		{libRoot, "mars_gen.go", renderMarsGateGRIB2(prov, g2Mars)},
	}
	for i := range outputs {
		formatted, err := format.Source([]byte(outputs[i].src))
		if err != nil {
			fatal("gofmt %s/%s: %v", outputs[i].dir, outputs[i].name, err)
		}
		outputs[i].src = string(formatted)
	}
	if *checkOnly {
		for _, o := range outputs {
			target := filepath.Join(o.dir, o.name)
			cur, err := os.ReadFile(target)
			if err != nil {
				fatal("check: %v", err)
			}
			if !bytes.Equal(cur, []byte(o.src)) {
				fatal("check: %s is out of date with %s/ (run: go run ./cmd/paramgen)", target, *dir)
			}
		}
		fmt.Println("paramgen -check: concepts_gen.go, paramdb/mars_gen.go, grib1/mars_gen.go and mars_gen.go in sync")
		return
	}
	for _, o := range outputs {
		if err := os.WriteFile(filepath.Join(o.dir, o.name), []byte(o.src), 0o644); err != nil {
			fatal("%v", err)
		}
	}
	fmt.Printf("paramgen: wrote %s/{concepts_gen.go,mars_gen.go} (grib2 shortName %d / name %d / paramId %d / units %d, grib1 %d/%d/%d/%d, tol %d/%d entries, mars %d/%d/%d)\n",
		*out, len(snG2), len(nameG2), len(pidG2), len(unitG2),
		len(snG1), len(nameG1), len(pidG1), len(unitG1), len(tolG2), len(tolG1),
		len(marsClass), len(marsType), len(marsStream))
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "paramgen: "+format+"\n", a...)
	os.Exit(1)
}

// ---------------------------------------------------------------- 数据模型

type cond struct{ key, val string }

// outEntry 是生成输出的一行：条件串下标 + 结果 token。四个概念各自独立
// 成表（edzw 等中心的四个文件条目数并不一致，lockstep 假设不成立），
// 优先级由追加顺序（概念链层级）编码。
type outEntry struct {
	tier   int // 概念链层级（tierPrimary/Ecmf/Legacy）
	conds  int // genConds 下标
	result string
}

type tierSource struct {
	centre string // "" = master definitions 目录
}

// --------------------------------------------------------------- .def 解析

// parseDefFile 解析一个概念 .def 文件为有序 (token, 条件) 列表。
// 格式：'token' = { key = value ; ... }，# 注释，missing() 哨兵。
// 键值对可以一行多个（typeOfLevelConcept.def 的风格），也可以一行一个
// （主概念文件的风格）；'…' = {…} 也允许整行闭合。
func parseDefFile(path string) ([]defEntry, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	var out []defEntry
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	line := 0
	var cur *defEntry
	closeCur := func() { cur = nil }
	for sc.Scan() {
		line++
		text := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		rest := text
		if strings.HasPrefix(text, "'") {
			if cur != nil {
				return nil, fmt.Errorf("%s line %d: entry started before previous closed", path, line)
			}
			end := strings.Index(text[1:], "'")
			if end < 0 {
				return nil, fmt.Errorf("%s line %d: malformed entry header %q", path, line, text)
			}
			token := text[1 : 1+end]
			tail := strings.TrimSpace(text[1+end+1:])
			brace := strings.Index(tail, "{")
			if !strings.HasPrefix(tail, "=") || brace < 0 {
				return nil, fmt.Errorf("%s line %d: malformed entry header %q", path, line, text)
			}
			out = append(out, defEntry{token: token})
			cur = &out[len(out)-1]
			rest = tail[brace+1:]
		} else if strings.HasPrefix(text, "}") {
			closeCur()
			continue
		}
		if cur == nil {
			continue // 概念文件头部的杂项（如 "concept X (...)" 声明行）
		}
		// 行内可能闭合条目：去掉结尾的 '}'（及其后内容）。
		if i := strings.LastIndex(rest, "}"); i >= 0 {
			rest = rest[:i]
			deferClose := true
			rest = strings.TrimSpace(rest)
			for _, frag := range strings.Split(rest, ";") {
				frag = strings.TrimSpace(frag)
				if frag == "" {
					continue
				}
				if err := cur.addFragment(frag); err != nil {
					return nil, fmt.Errorf("%s line %d: %v", path, line, err)
				}
			}
			closeCur()
			_ = deferClose
			continue
		}
		for _, frag := range strings.Split(rest, ";") {
			frag = strings.TrimSpace(frag)
			if frag == "" {
				continue
			}
			if err := cur.addFragment(frag); err != nil {
				return nil, fmt.Errorf("%s line %d: %v", path, line, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if cur != nil {
		return nil, fmt.Errorf("%s: unterminated entry", path)
	}
	return out, nil
}

type defEntry struct {
	token string
	conds []cond
}

func (d *defEntry) addFragment(frag string) error {
	eq := strings.Index(frag, "=")
	if eq < 0 {
		return fmt.Errorf("not key=value: %q", frag)
	}
	key := strings.TrimSpace(frag[:eq])
	val := normalizeValue(frag[eq+1:])
	if key == "" {
		return fmt.Errorf("empty key in %q", frag)
	}
	if strings.ContainsAny(key, ";=") || strings.ContainsAny(val, ";=") {
		return fmt.Errorf("key/value contains packed-format separator: %q=%q", key, val)
	}
	for _, c := range d.conds {
		if c.key == key {
			return fmt.Errorf("duplicate condition key %q in one entry", key)
		}
	}
	d.conds = append(d.conds, cond{key: key, val: val})
	return nil
}

// sameConds 报告两个条目的条件序列是否逐项一致（lockstep 校验用）。
func (a defEntry) sameConds(b defEntry) bool {
	if len(a.conds) != len(b.conds) {
		return false
	}
	for i := range a.conds {
		if a.conds[i] != b.conds[i] {
			return false
		}
	}
	return true
}

// normalizeValue 归一条件值：去引号；missing() → 哨兵；数字 → 规范十进制。
func normalizeValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "missing()" {
		return missingToken
	}
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	if n, err := strconv.Atoi(v); err == nil {
		return strconv.Itoa(n)
	}
	return v
}

// condPool 把条件串去重 intern 成 genConds 池：四个并行概念文件的条件
// 高度重复，池化后生成代码体积减半以上。
type condPool struct {
	ids map[string]int
	all []string
}

func newCondPool() *condPool { return &condPool{ids: map[string]int{}} }

func (p *condPool) intern(packed string) int {
	if i, ok := p.ids[packed]; ok {
		return i
	}
	i := len(p.all)
	p.all = append(p.all, packed)
	p.ids[packed] = i
	return i
}

// emit 把一个概念文件的有序条目转成输出条目；mtg2（层级开关）与
// centreCond（本地概念按消息中心圈定）非空时作为额外条件前缀。
// nil 输入（文件缺失）透传为 nil。
func (p *condPool) emit(list []defEntry, tier int, mtg2, centreCond string) []outEntry {
	if list == nil {
		return nil
	}
	prefix := centreCond
	if mtg2 != "" {
		prefix = mtg2 + ";" + prefix
	}
	out := make([]outEntry, 0, len(list))
	for _, e := range list {
		var sb strings.Builder
		sb.WriteString(prefix)
		for j, c := range e.conds {
			if j > 0 {
				sb.WriteByte(';')
			}
			sb.WriteString(c.key)
			sb.WriteByte('=')
			sb.WriteString(c.val)
		}
		out = append(out, outEntry{tier: tier, conds: p.intern(sb.String()), result: e.token})
	}
	return out
}

// single 解析单结果概念（typeOfLevel）。
func (p *condPool) single(path string, tier int) []outEntry {
	list := mustParseOptional(path)
	if list == nil {
		fatal("%s: file missing", path)
	}
	return p.emit(list, tier, "", "")
}

// mustParseOptional 解析可选文件：缺失返回 nil，其它错误报错。
func mustParseOptional(path string) []defEntry {
	list, err := parseDefFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		fatal("%v", err)
	}
	return list
}

// parseC1Table 解析 common/c-1.table（CCT-1）：每行
// "centre mnemonic name…"（第一个 token 是中心号，第二个是助记符）。
// 返回助记符 → 中心号**列表**：一个助记符可能对应多个中心号（c-1.table 里
// lfpw 同时是 84 和 85），而 ecCodes 的 conceptsLocalDirAll 用助记符选目录
// （"localConcepts/[centre:s]"），所以这些号都要能命中同一份本地概念。
func parseC1Table(path string) map[string][]int {
	fh, err := os.Open(path)
	if err != nil {
		fatal("%v", err)
	}
	defer fh.Close()
	out := map[string][]int{}
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		text := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		out[fields[1]] = append(out[fields[1]], n)
	}
	if len(out) == 0 {
		fatal("%s: no centres parsed", path)
	}
	return out
}

// grib1TableEntry 是中心本地 GRIB1 参数表的一行。
type grib1TableEntry struct {
	conds, sn, name, unit string
}

// parseGrib1CentreTables 解析 grib1/2.<centre>.<version>.table：每行
// "code shortname name (unit)"，最后一个小括号组是单位。产出带
// centre/table2Version/indicatorOfParameter 条目的条目。
func parseGrib1CentreTables(dir string) []grib1TableEntry {
	files, err := filepath.Glob(filepath.Join(dir, "2.*.*.table"))
	if err != nil {
		fatal("%v", err)
	}
	sort.Strings(files)
	var out []grib1TableEntry
	for _, f := range files {
		base := filepath.Base(f) // "2.98.128.table"
		parts := strings.Split(strings.TrimSuffix(base, ".table"), ".")
		if len(parts) != 3 {
			continue
		}
		centre, err1 := strconv.Atoi(parts[1])
		version, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil {
			continue
		}
		prefix := fmt.Sprintf("centre=%d;table2Version=%d;", centre, version)
		fh, err := os.Open(f)
		if err != nil {
			fatal("%v", err)
		}
		sc := bufio.NewScanner(fh)
		line := 0
		for sc.Scan() {
			line++
			text := strings.TrimSpace(strings.TrimSuffix(sc.Text(), "\r"))
			if text == "" || strings.HasPrefix(text, "#") {
				continue
			}
			fields := strings.SplitN(text, " ", 3)
			if len(fields) < 3 {
				fmt.Fprintf(os.Stderr, "paramgen: note: %s line %d: skipped %q\n", base, line, text)
				continue
			}
			code, err := strconv.Atoi(fields[0])
			if err != nil {
				fmt.Fprintf(os.Stderr, "paramgen: note: %s line %d: non-numeric code %q skipped\n", base, line, fields[0])
				continue
			}
			rest := fields[2]
			// "name (unit)"：最后一个小括号组是单位；没有括号组就没有单位。
			name, unit := rest, ""
			if i := strings.LastIndex(rest, "("); i >= 0 && strings.HasSuffix(rest, ")") {
				name = strings.TrimSpace(rest[:i])
				unit = rest[i+1 : len(rest)-1]
			}
			out = append(out, grib1TableEntry{
				conds: prefix + "indicatorOfParameter=" + strconv.Itoa(code),
				sn:    fields[1],
				name:  name,
				unit:  unit,
			})
		}
		fh.Close()
	}
	return out
}

// --------------------------------------------------------------- 输出

func render(prov string, pool *condPool,
	snG2, nameG2, pidG2, unitG2 []outEntry,
	snG1, nameG1, pidG1, unitG1 []outEntry,
	tolG2, tolG1 []outEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by cmd/paramgen from paramdata/ (%s).\n// DO NOT EDIT.\n\npackage paramdb\n\n", prov)
	b.WriteString("// 每个概念是一条有序条目表（层级优先级已编码进追加顺序）：层内 best-match，\n")
	b.WriteString("// 条件最多者胜、平手取后者。conds 是 \"k=v;k=v\" 打包串在 genConds 里的\n")
	b.WriteString("// 下标，由包内 init 解开。\n\n")
	b.WriteString("type genEntry struct {\n\ttier   int\n\tconds  int\n\tresult string\n}\n\n")
	fmt.Fprintf(&b, "// 条件池：%d 个去重后的条件串。\nvar genConds = []string{\n", len(pool.all))
	for _, c := range pool.all {
		fmt.Fprintf(&b, "\t%s,\n", strconv.Quote(c))
	}
	b.WriteString("}\n\n")
	writeList := func(name string, list []outEntry) {
		fmt.Fprintf(&b, "var %s = []genEntry{\n", name)
		for _, e := range list {
			fmt.Fprintf(&b, "\t{%d, %d, %s},\n", e.tier, e.conds, strconv.Quote(e.result))
		}
		b.WriteString("}\n\n")
	}
	writeList("genShortNameGrib2", snG2)
	writeList("genNameGrib2", nameG2)
	writeList("genParamIdGrib2", pidG2)
	writeList("genUnitsGrib2", unitG2)
	writeList("genShortNameGrib1", snG1)
	writeList("genNameGrib1", nameG1)
	writeList("genParamIdGrib1", pidG1)
	writeList("genUnitsGrib1", unitG1)
	writeList("genTypeOfLevelGrib2", tolG2)
	writeList("genTypeOfLevelGrib1", tolG1)
	return b.String()
}

// parseMarsTable 解析 mars/*.table：每行 "<code> <abbrev> <description>"，
// 空行与 # 注释跳过。返回 code → 缩写，供 mars_gen.go 使用。
func parseMarsTable(path string) (map[int]string, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	out := map[int]string{}
	sc := bufio.NewScanner(fh)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s:%d: want \"<code> <abbrev> [description]\", got %q", path, lineNo, line)
		}
		code, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: bad code %q", path, lineNo, fields[0])
		}
		out[code] = fields[1]
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// renderMars 生成 mars_gen.go：MARS 标签的三张 code → 缩写表。
func renderMars(prov string, class, typ, stream map[int]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by cmd/paramgen from paramdata/mars/ (%s).\n// DO NOT EDIT.\n\npackage paramdb\n\n", prov)
	b.WriteString("// MARS 标签表：GRIB 里传的是整数 code，ecCodes 的 marsClass/marsType/marsStream\n")
	b.WriteString("// 对外给的是缩写 token（od/fc/enfo …）。来源 paramdata/mars/*.table。\n\n")
	writeMap := func(name, doc string, src map[int]string) {
		fmt.Fprintf(&b, "// %s %s\nvar %s = map[int]string{\n", name, doc, name)
		keys := make([]int, 0, len(src))
		for k := range src {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\t%d: %s,\n", k, strconv.Quote(src[k]))
		}
		b.WriteString("}\n\n")
	}
	writeMap("marsClassAbbrev", "映射 mars/class.table 的 code → 缩写。", class)
	writeMap("marsTypeAbbrev", "映射 mars/type.table 的 code → 缩写。", typ)
	writeMap("marsStreamAbbrev", "映射 mars/stream.table 的 code → 缩写。", stream)
	return b.String()
}

// MARS 标签（marsClass / marsType / marsStream / experimentVersionNumber）不是
// 每条消息都有：ecCodes 把它们放在各中心的本地定义模板里，只有真正声明了这四
// 个键的模板才提供它们。读取方必须按同一规则门控，否则会把普通字节误读成 MARS
// 值。生成器据此从 vendored 定义推导出两张门控表（见 renderMarsGate*）：
//
//	GRIB1: grib1/local.98.<N>.def —— 按本地定义号（PDS octet 41）
//	GRIB2: grib2/local.<centre>.def —— 按消息中心
//
// 「声明了」的判据是模板里出现 `codetable[..] marsClass` —— 可以是模板自身，
// 也可以沿 `template`/`template_nofail` 引用（例如 grib1/mars_labeling.def）。
// 这条判据把两个易错点一并处理掉：
//
//   - grib1/local.98.192.def 声明的是 thisMarsClass 等别名键，没有 marsClass，
//     因此正确地被判为「不提供 MARS 标签」；
//   - grib2/local.82.def 引用的 grib2/mars_labeling.82.def 只声明别名，也没有
//     marsClass，因此中心 82 同样被排除。
var (
	reMarsClassDecl = regexp.MustCompile(`(?m)^\s*codetable\s*\[\s*\d+\s*\]\s+marsClass\b`)
	// 只跟随静态路径：带 [..] 的（local.98.[N].def 之类）跟随不了，而它们恰好
	// 也不是 MARS 标签的来源。
	reMarsTemplateRef = regexp.MustCompile(`(?m)^\s*template(?:_nofail)?\s+\w+\s+"([^"\[\]]+)"`)
	reG2CentreLocal   = regexp.MustCompile(`^local\.(\d+)\.def$`)
)

// declaresMarsClass 报告 rel（相对 definitions 根，用 / 分隔）是否声明了
// marsClass 码表——直接声明，或沿 template 引用间接声明。
func declaresMarsClass(root, rel string, seen map[string]bool) bool {
	if seen[rel] {
		return false
	}
	seen[rel] = true
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	if reMarsClassDecl.Match(src) {
		return true
	}
	for _, m := range reMarsTemplateRef.FindAllSubmatch(src, -1) {
		if declaresMarsClass(root, string(m[1]), seen) {
			return true
		}
	}
	return false
}

// parseMarsGate 推导 MARS 标签门控表：GRIB1 的本地定义号集合与 GRIB2 的中心集合。
func parseMarsGate(dir string) (map[int]bool, map[int]bool, error) {
	g1 := map[int]bool{}
	for _, p := range mustGlob(filepath.Join(dir, "grib1", "local.98.*.def")) {
		name := filepath.Base(p)
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "local.98."), ".def"))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: 本地定义号解析失败: %w", p, err)
		}
		if declaresMarsClass(dir, "grib1/"+name, map[string]bool{}) {
			g1[n] = true
		}
	}
	g2 := map[int]bool{}
	for _, p := range mustGlob(filepath.Join(dir, "grib2", "local.*.def")) {
		name := filepath.Base(p)
		m := reG2CentreLocal.FindStringSubmatch(name)
		if m == nil {
			continue // local.<助记符>.<N>.def / local.destine.*.def 等：不是中心模板
		}
		c, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, nil, fmt.Errorf("%s: 中心号解析失败: %w", p, err)
		}
		if declaresMarsClass(dir, "grib2/"+name, map[string]bool{}) {
			g2[c] = true
		}
	}
	if len(g1) == 0 || len(g2) == 0 {
		return nil, nil, fmt.Errorf("MARS 门控表为空（grib1=%d, grib2=%d）——vendored 子集不完整？", len(g1), len(g2))
	}
	return g1, g2, nil
}

// mustGlob 是 filepath.Glob 的"失败即报错"版本。
func mustGlob(pattern string) []string {
	paths, err := filepath.Glob(pattern)
	if err != nil {
		fatal("glob %s: %v", pattern, err)
	}
	return paths
}

// renderMarsGateGRIB1 生成 grib1/mars_gen.go。
func renderMarsGateGRIB1(prov string, defs map[int]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by cmd/paramgen from paramdata/grib1/local.98.*.def (%s).\n// DO NOT EDIT.\n\npackage grib1\n\n", prov)
	b.WriteString("// marsLocalDefinitions 是提供了 MARS 标签（marsClass / marsType /\n")
	b.WriteString("// marsStream / experimentVersionNumber）的 ECMWF 本地定义号集合。\n")
	b.WriteString("//\n")
	b.WriteString("// 依据：ecCodes 的 grib1/section.1.def 在 centre 或 subCentre 为 ECMWF(98)\n")
	b.WriteString("// 时按 grib1/local.98.[localDefinitionNumber].def 取模板；这些模板里有一部\n")
	b.WriteString("// 分 template 了 grib1/mars_labeling.def，那里才是声明四个 MARS 键的地方。\n")
	b.WriteString("// 本表是逐个模板解析引用后的结论（见 cmd/paramgen 的 declaresMarsClass）。\n")
	b.WriteString("var marsLocalDefinitions = map[int]bool{\n")
	writeIntSet(&b, defs)
	b.WriteString("}\n")
	return b.String()
}

// renderMarsGateGRIB2 生成 grib/mars_gen.go。
func renderMarsGateGRIB2(prov string, centres map[int]bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// Code generated by cmd/paramgen from paramdata/grib2/local.*.def (%s).\n// DO NOT EDIT.\n\npackage grib\n\n", prov)
	b.WriteString("// marsCentres 是本地定义声明了 MARS 标签（marsClass / marsType /\n")
	b.WriteString("// marsStream / experimentVersionNumber）的中心号集合。\n")
	b.WriteString("//\n")
	b.WriteString("// 依据：ecCodes 的 grib2/section.2.def 在 grib2LocalSectionNumber != 0 时按\n")
	b.WriteString("// grib2/local.[centreForLocal].def 取模板。只有这些模板（含其 template\n")
	b.WriteString("// 引用）声明了 marsClass；其余中心（例如 82 走的 mars_labeling.82.def）\n")
	b.WriteString("// 只声明别名，并没有 marsClass。\n")
	b.WriteString("var marsCentres = map[int]bool{\n")
	writeIntSet(&b, centres)
	b.WriteString("}\n")
	return b.String()
}

func writeIntSet(b *strings.Builder, set map[int]bool) {
	keys := make([]int, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		fmt.Fprintf(b, "\t%d: true,\n", k)
	}
}

func readProvenance(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fatal("provenance: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		fatal("provenance %s is empty", path)
	}
	return strings.Join(lines, "; ")
}
