// Package paramdb 回答 ecCodes 用 "concept" 机制回答的参数问题：
// shortName、name、paramId、units 与 typeOfLevel。
//
// 数据来自 paramdata/（vendored、版本钉死的 ecCodes definitions 子集，
// 见 paramdata/PROVENANCE.txt），由 cmd/paramgen 生成 concepts_gen.go。
// 每个概念是一张有序条目表：每条 = 一组条件（消息其它键的取值）+ 结果
// token；层内取"条件全中且条件数最多"的条目（平手取后者），这正是
// ecCodes 的匹配语义，也是 shortName 能区分"2 米温度(2t)"与"等压面温度(t)"
// （同一参数号、不同层）的原因。
//
// 查找入口接受 Map（键值对，键名与 ecCodes 相同）；QueryFrom 从已解析的
// grib.Message 构造它。没匹配上返回 ok=false（本包绝不猜）；要逐字复刻
// ecCodes 的键值（"unknown"/0）请用 ShortNameOrUnknown 一族，见 Unknown。
//
// 覆盖范围与 ecCodes 的差异（都有意为之，详见包内各处注释）：
//   - 只实现了概念条件涉及的"核心键"；概率模板的 probabilityType 与上下限
//     （scaleFactorOf*/scaledValueOf*Limit）已由 QueryFrom 提供，化学/气溶胶
//     参数键、productionStatusOfProcessedData/generation（伪中心选择）也已提供。
//     MARS 命名空间键（marsClass/marsType/marsStream 及 class/type/stream 别名、
//     experimentVersionNumber）已实现（mars.go）。标签的有无由 grib 包按 ecCodes
//     的本地定义模板链判定（grib/mars.go、grib1/mars.go，门控表由 cmd/paramgen
//     从 vendored 定义生成），本包只做码 → 键的映射；注意 codetable 三者给的是
//     **整数 code**（ecCodes 的 datasetForLocal 条件即写整数），缩写走别名键。
//   - 全部伪中心数据集目录均已接入：靠 productionStatusOfProcessedData 的
//     tigge/s2s/uerra/destine 等，以及靠 marsClass 的 era/era6/cerise/
//     hydro_legacy/nextgems（hydro、fire 在 vendored 子集里是空目录）。
//   - GRIB1 覆盖 WMO 表 2 版本 1-3、14 个中心的
//     `grib1/localConcepts/<centre>`（含 ECMWF 128/173，提供逐字 name 与
//     paramId），以及 47 张中心 `.table` 作为没有本地概念文件的中心的
//     显示名兜底。ecCodes 装配里的其余边角（chem split、GRIB1 localConcepts
//     概念链、4.112/4.123 概率字段）已实现。
//   - isobaric 层的 pressureUnits 按 ecCodes 默认取 "hPa"。
//
// Package paramdb answers the parameter questions ecCodes answers with its
// "concept" mechanism: shortName, name, paramId, units and typeOfLevel.
package paramdb

import (
	"strconv"
	"strings"
)

// Missing 表示"消息里存在该键但取值缺失"（ecCodes 的 missing 语义），
// 概念条件里的 missing() 只与它匹配。QueryFrom 会用它填充缺失的
// scaleFactorOfFirstFixedSurface 之类的键。
const Missing = "\x00missing"

// Unknown 是 ecCodes 在概念链完全没有命中时给出的字面量：shortName、name、
// units 三个键都是 "unknown"，paramId 是 0（不是字符串）。ecCodes 2.47 与 2.50
// 都是这个行为，例如把一条 GRIB2 消息的 parameterCategory/parameterNumber 改成
// 表里都没有的值后：
//
//	codes_get(h, "shortName")  -> 'unknown'
//	codes_get(h, "name")       -> 'unknown'
//	codes_get(h, "units")      -> 'unknown'
//	codes_get(h, "paramId")    -> 0
//
// 本包的 ShortName/Name/Units 保持 (值, ok) 形状：ok=false 才携带"没命中"这条
// 信息，这是调用方判断"消息里到底有没有这个参数"的唯一依据，所以不把返回值改成
// Unknown。要逐字复刻 ecCodes 的键值（例如 gribdump -json 的对照输出）请用
// ShortNameOrUnknown / NameOrUnknown / UnitsOrUnknown / ParamIDOrZero。
const Unknown = "unknown"

// ShortNameOrUnknown 是 ecCodes 的 shortName 键视图：没命中时给 Unknown。
func ShortNameOrUnknown(q Map) string {
	if v, ok := ShortName(q); ok {
		return v
	}
	return Unknown
}

// NameOrUnknown 是 ecCodes 的 name 键视图：没命中时给 Unknown。
func NameOrUnknown(q Map) string {
	if v, ok := Name(q); ok {
		return v
	}
	return Unknown
}

// UnitsOrUnknown 是 ecCodes 的 units 键视图：没命中时给 Unknown。
func UnitsOrUnknown(q Map) string {
	if v, ok := Units(q); ok {
		return v
	}
	return Unknown
}

// ParamIDOrZero 是 ecCodes 的 paramId 键视图：没命中时给 0（ParamID 本身也返回
// 0，这里只是把 ecCodes 的兜底写成显式契约）。
func ParamIDOrZero(q Map) int {
	v, _ := ParamID(q)
	return v
}

// TypeOfLevelOrUnknown 是 ecCodes 的 typeOfLevel 键视图：没命中时给 Unknown。
// 把 typeOfFirstFixedSurface 改成表里没有的 200 或 255 时，ecCodes 的
// typeOfLevel 就是 'unknown'（2.47 与 2.50 相同）。
func TypeOfLevelOrUnknown(q Map) string {
	if v, ok := TypeOfLevel(q); ok {
		return v
	}
	return Unknown
}

// Map 是一次查询的键值对：键名与 ecCodes 相同（discipline、
// parameterCategory、typeOfFirstFixedSurface、…），值统一为字符串
// （整数用十进制）。QueryFrom 构造标准键集；手造 Map 也允许。
type Map map[string]string

// kv 是一条条件。
type kv struct {
	key string
	val string
}

// entry 是概念表里的一行：条件组 + 结果 token。
type entry struct {
	conds  []kv
	result string
}

// concept 是一个层级内的有序条目表；lookup 做 best-match：在全部条件
// 都满足的候选里取条件数最多者，平手取后者。
//
// 依据 ecCodes 2.50.0 src/eccodes/accessor/Concept.cc 的 concept_evaluate：
// 逐条目统计完全命中的条件数 cnt，`if (cnt >= match) { match = cnt; best = … }`
// ——即 best-match，平手时后面的条目赢。这同时解释了两个现象：
// 泛化 't'（3 条件）永远输给带层条件的 '2t'（6 条件）；以及
// 'tpg1'（8 条件）虽然排在文件开头，仍然赢过后面的 'tp'（5 条件）。
// （文件头注释 "get returns the LAST match" 说的是重复 token 的集合赋值，
// 与 get 的 best-match 语义是两回事。）
type concept []entry

func build(list []genEntry) concept {
	c := make(concept, len(list))
	for i, e := range list {
		packed := genConds[e.conds]
		var conds []kv
		for _, frag := range strings.Split(packed, ";") {
			if frag == "" {
				continue // 条件前缀为空时产生的空片段
			}
			eq := strings.Index(frag, "=")
			conds = append(conds, kv{key: frag[:eq], val: frag[eq+1:]})
		}
		c[i] = entry{conds: conds, result: e.result}
	}
	return c
}

// tiers 是概念链：层与层之间"第一个有命中的层胜出"（fallback 语义——
// 短路链 shortName → shortNameECMF → shortNameLegacyECMF 只在前面整层无
// 命中时才走下一层），层内 last-match（见上）。
type tiers []concept

func (t tiers) lookup(q Map) (string, bool) {
	for _, c := range t {
		if v, ok := c.lookup(q); ok {
			return v, true
		}
	}
	return "", false
}

// buildTiers 把生成条目按 tier 字段分组并各自展开；生成数据按层级升序
// 追加，分组保持该顺序。
func buildTiers(list []genEntry) tiers {
	byTier := map[int][]genEntry{}
	var order []int
	for _, e := range list {
		if _, ok := byTier[e.tier]; !ok {
			order = append(order, e.tier)
		}
		byTier[e.tier] = append(byTier[e.tier], e)
	}
	out := make(tiers, 0, len(order))
	for _, t := range order {
		out = append(out, build(byTier[t]))
	}
	return out
}

// lookup 在一个层级内做 best-match：全部条件命中的候选里条件数最多者
// 胜出，平手取后者（对齐 ecCodes concept_evaluate 的 `cnt >= match`）。
func (c concept) lookup(q Map) (string, bool) {
	best := ""
	bestCnt := -1
	found := false
	for i := range c {
		if cnt, ok := c[i].matchCount(q); ok {
			if cnt >= bestCnt { // 平手取后者，与 ecCodes 的 >= 一致
				best, bestCnt, found = c[i].result, cnt, true
			}
		}
	}
	return best, found
}

// matchCount 返回该条目全部条件命中的数量；任一条件不满足则 ok=false。
func (e *entry) matchCount(q Map) (int, bool) {
	cnt := 0
	for _, c := range e.conds {
		v, ok := q[c.key]
		if !ok || !valueEq(v, c.val) {
			return 0, false
		}
		cnt++
	}
	return cnt, true
}

// matches 是 matchCount 的布尔视图。
func (e *entry) matches(q Map) bool {
	_, ok := e.matchCount(q)
	return ok
}

// valueEq 优先按整数比较（查询方的 "0" 与条件方的 "0" 已规范化，但手造
// Map 可能带来 "00" 这类写法），否则按字符串比较。
func valueEq(a, b string) bool {
	if a == b {
		return true
	}
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	return aerr == nil && berr == nil && ai == bi
}

// 概念表在 init 里从生成数据解开一次。
var (
	shortNameG2 = buildTiers(genShortNameGrib2)
	nameG2      = buildTiers(genNameGrib2)
	paramIdG2   = buildTiers(genParamIdGrib2)
	unitsG2     = buildTiers(genUnitsGrib2)

	shortNameG1 = buildTiers(genShortNameGrib1)
	nameG1      = buildTiers(genNameGrib1)
	paramIdG1   = buildTiers(genParamIdGrib1)
	unitsG1     = buildTiers(genUnitsGrib1)

	typeOfLevelG2 = buildTiers(genTypeOfLevelGrib2)
	typeOfLevelG1 = buildTiers(genTypeOfLevelGrib1)
)

// ShortName 返回参数短名（ecCodes 的 shortName 键）：如 2t、t、tp、msl。
// GRIB2 与 GRIB1 的键空间不相交，两表按序查找即可。
func ShortName(q Map) (string, bool) {
	if v, ok := shortNameG2.lookup(q); ok {
		return v, true
	}
	return shortNameG1.lookup(q)
}

// Name 返回参数长名（ecCodes 的 name 键）：如 "2 metre temperature"。
func Name(q Map) (string, bool) {
	if v, ok := nameG2.lookup(q); ok {
		return v, true
	}
	return nameG1.lookup(q)
}

// Units 返回 ecCodes 拼写的单位（如 K、m s-1、~）。它与 WMO 规范表
// （spec.ParameterUnit）的 WMO 拼写刻意不同——这里的目标是与 ecCodes 的
// units 键逐字一致。
func Units(q Map) (string, bool) {
	if v, ok := unitsG2.lookup(q); ok {
		return v, true
	}
	return unitsG1.lookup(q)
}

// ParamID 返回 ECMWF/MARS 参数号（ecCodes 的 paramId 键，如 2t → 167）。
func ParamID(q Map) (int, bool) {
	if v, ok := paramIdG2.lookup(q); ok {
		return atoi(v)
	}
	return atoiFirst(paramIdG1.lookup(q))
}

// TypeOfLevel 返回层的 ecCodes 名字（isobaricInhPa、heightAboveGround、
// surface、…）。isobaric 层按 ecCodes 默认视 pressureUnits 为 hPa。
func TypeOfLevel(q Map) (string, bool) {
	if v, ok := typeOfLevelG2.lookup(q); ok {
		return v, true
	}
	return typeOfLevelG1.lookup(q)
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func atoiFirst(s string, ok bool) (int, bool) {
	if !ok {
		return 0, false
	}
	return atoi(s)
}
