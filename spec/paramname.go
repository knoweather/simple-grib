package spec

import (
	"sort"
	"strconv"
)

// ParamNameRef 是一次「按消息声明的表版本查表 4.2 题名」的完整输入：参数三元组
// 加上第 1 节声明的表版本与中心。
//
// ParamNameRef is the complete input of one version-aware table 4.2 title
// lookup: the parameter triple plus the table versions and originating centre
// the message declares in section 1.
type ParamNameRef struct {
	// Discipline / Category / Number 是 GRIB2 的参数三元组（第 0 节第 7 八位组
	// 与第 4 节第 10、11 八位组）。
	Discipline int
	Category   int
	Number     int
	// MasterVersion 是第 1 节第 10 八位组（ecCodes 的 tablesVersion /
	// masterTablesVersionNumber），决定主表目录 grib2/tables/<v>。
	//
	// MasterVersion is section 1 octet 10 (ecCodes' tablesVersion /
	// masterTablesVersionNumber): it selects the master directory
	// grib2/tables/<v>.
	MasterVersion int
	// LocalVersion 是第 1 节第 11 八位组（localTablesVersion）。0 与 255 表示
	// 「不用本地表」——与 ecCodes section.1.def 的
	// `if (localTablesVersion != 0 and localTablesVersion != 255)` 一致。
	//
	// LocalVersion is section 1 octet 11 (localTablesVersion). 0 and 255 mean
	// "no local tables", matching ecCodes' section.1.def.
	LocalVersion int
	// Centre 是第 1 节第 6-7 八位组（表 CCT-11 的中心号）。ecCodes 用该中心的
	// c-11 助记符（如 "ecmf"）拼出本地表目录
	// grib2/tables/local/<助记符>/<localTablesVersion>。
	//
	// Centre is section 1 octets 6-7 (CCT-11 centre code). ecCodes names the
	// local tables directory with that centre's c-11 mnemonic ("ecmf", …).
	Centre int
}

// ParameterNameFor 返回 ecCodes 键 "parameterName" 在 GRIB2 消息上的取值：消息
// **自己声明的**表版本里，参数三元组对应那一行的题名（title）。表版本来自第 1 节
// （见 ParamNameRef），与 ParameterName 固定使用最新 WMO 表不同。
//
// ParameterNameFor returns what ecCodes' "parameterName" key yields for a GRIB2
// message: the title of the table 4.2 row for the triple, taken from the table
// version the *message itself* declares (see ParamNameRef) — unlike
// ParameterName, which always uses the newest WMO tables.
//
// 解析顺序照抄 ecCodes 的 codetable 机制（definitions/grib2/section.1.def +
// templates/template.4.parameter.def + src/eccodes/accessor/Codetable.cc）：
//
//  1. masterDir = grib2/tables/<MasterVersion>；MasterVersion 大于 vendored 的
//     最新版本（pnameLatestVersion，ecCodes 2.50.0 的 boot.def 里
//     tablesVersionLatest = 37）时**钳制**到最新版本，与 section.1.def 的
//     `if (tablesVersion > tablesVersionLatest)` 一致。
//  2. 表名恒为 4.2.<discipline>.<category>（template.4.parameter.def 的
//     tablename），**没有**「4.2.<d>.<c> → 4.2.<d> → 4.2」这类层次回退：
//     该文件不存在就是查不到。ParameterName（最新表版本、扁平回退）是另一套
//     语义，不要混用。
//  3. 本地表：LocalVersion 既不等于 0 也不等于 255 时，叠加
//     localDir = grib2/tables/local/<centre 的 c-11 助记符>/<LocalVersion> 下的同名
//     文件。ecCodes 先装主表、后装本地表，且对已有 code 直接跳过后续文件
//     （Codetable.cc 的 grib_load_codetable），所以**同码时主表胜**，本地表只
//     补主表没有的码。消息中心在 c-11 表里没有助记符时 localDir 是一个不存在的
//     目录，等价于没有本地表。
//  4. 码在表里查不到（表文件缺失、版本目录缺失、或该版本此码未定义）时，
//     ecCodes 的 CodetableTitle::unpack_string 退化成**十进制码的字符串**
//     （"224"、"13"、"0"），不是空串、也不是最新表的题名。本函数照此返回。
//     长度为 1 字节的 parameterNumber 因此永远能得到非空结果。
//
// Lookup order, mirroring ecCodes (section.1.def, template.4.parameter.def,
// Codetable.cc): the master directory is grib2/tables/<MasterVersion>, clamped
// to pnameLatestVersion when the declared version is newer (section.1.def's
// `if (tablesVersion > tablesVersionLatest)`); the file name is always
// 4.2.<discipline>.<category> with no tiered fallback; local tables from
// grib2/tables/local/<centre mnemonic>/<LocalVersion> are merged in *after* the
// master file and only fill codes the master leaves empty; and a code that
// resolves nowhere renders as the decimal code, exactly like
// CodetableTitle::unpack_string.
func ParameterNameFor(ref ParamNameRef) string {
	if ref.Number < 0 || ref.Number > 255 {
		return strconv.Itoa(ref.Number)
	}
	master := ref.MasterVersion
	if master > pnameLatestVersion {
		master = pnameLatestVersion
	}
	if master >= 0 && ref.Discipline >= 0 && ref.Discipline <= 255 && ref.Category >= 0 && ref.Category <= 255 {
		if idx, ok := pnameMasterIndex[uint32(master)<<16|uint32(ref.Discipline)<<8|uint32(ref.Category)]; ok {
			if title, ok := pnameTitle(idx, ref.Number); ok {
				return title
			}
		}
	}
	// 本地表只在 localTablesVersion ∉ {0, 255} 时参与，且只补主表没有的码。
	if ref.LocalVersion != 0 && ref.LocalVersion != 255 && ref.Centre > 0 {
		key := uint64(ref.Centre)<<32 | uint64(ref.LocalVersion)<<24 |
			uint64(ref.Discipline)<<16 | uint64(ref.Category)
		if idx, ok := pnameLocalIndex[key]; ok {
			if title, ok := pnameTitle(idx, ref.Number); ok {
				return title
			}
		}
	}
	return strconv.Itoa(ref.Number)
}

// pnameTitle 在 pnameTables[idx] 里按 code 精确取题名；该表的 code 严格升序
// （生成器保证），故用二分。code 在表外时 ok=false —— 调用方据此退化到十进制码。
func pnameTitle(idx int32, code int) (string, bool) {
	if idx < 0 || int(idx) >= len(pnameTables) {
		return "", false
	}
	t := pnameTables[idx]
	i := sort.Search(len(t), func(i int) bool { return int(t[i].code) >= code })
	if i < len(t) && int(t[i].code) == code {
		return t[i].title, true
	}
	return "", false
}
