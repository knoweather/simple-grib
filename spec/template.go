package spec

import (
	"strconv"
	"sync"
)

// TemplateDef 描述一个 WMO GRIB2 section 模板：其字段带绝对位置（基于 0，
// 相对“section octet 1”）与种类。它是生成的 genTemplates 切片之上的公开视图；
// 实例被共享，不可被修改。

// Kind 表示字段字节应被如何读取的种类。
type Kind byte

const (
	KindU8       Kind = iota // 1 字节，无符号
	KindU16                  // 2 字节，大端无符号
	KindU32                  // 4 字节，大端无符号
	KindF32                  // 4 字节，IEEE 754 binary32（大端）
	KindVariable             // 变长尾部（永远是最后一个固定字段）
	KindSymbolic             // 参数化/条件位置；无固定字节
)

// Field 是模板定义的一行。Start 是 section 字节里的基于 0 偏移（octet 1 = 4 字节
// 长度前缀的首字节）；Symbolic 字段的 Start 为 -1。Count 是字节数；0 表示无界
// （Variable）。TableSec/TableFlag 标识该值所索引的代码表（TableSec==255 表示
// 一张 CCT 表，TableFlag 是其编号）；字段无表引用时两者都为 0。Expr 承载
// Symbolic 字段的原始 WMO 表达式，以及供诊断用的生成器标记（"sameas:..."、
// "tabref:..."）。
type Field struct {
	Start     int
	Count     int
	Kind      Kind
	TableSec  int
	TableFlag int
	Expr      string
}

// TemplateDef 是公开的模板描述符。
type TemplateDef struct {
	ID     string // "3.0"、"5.4" …
	Name   string
	Status string
	Fields []Field
}

// templatesByKey 由 init() 从生成的 genTemplates 切片构建；RegisterLocalTable
// 可能在运行时追加本地使用条目（由 mu 保护）。
var (
	mu             sync.RWMutex
	templatesByKey = map[string]*TemplateDef{}
)

func init() {
	for _, gt := range genTemplates {
		t := &TemplateDef{ID: gt.id, Name: gt.name, Status: gt.status}
		for _, f := range gt.fields {
			t.Fields = append(t.Fields, Field{
				Start:     int(f.start),
				Count:     int(f.count),
				Kind:      Kind(f.kind),
				TableSec:  int(f.tsec),
				TableFlag: int(f.tflag),
				Expr:      f.expr,
			})
		}
		templatesByKey[t.ID] = t
	}
}

// Template 返回 (section, number) 对应的定义，包含通过 RegisterLocalTable
// 注册的本地使用模板。未注册的本地使用号与未知组合返回 ok=false；调用方必须
// 处理这种情况（仍可通过 Section.Data 做仅元数据的访问）。
func Template(section, number int) (*TemplateDef, bool) {
	mu.RLock()
	defer mu.RUnlock()
	t, ok := templatesByKey[strconv.Itoa(section)+"."+strconv.Itoa(number)]
	return t, ok
}

// EndIntervalOffset returns the 0-based offset of the Year field of the
// "time of end of overall time interval" timestamp inside a section, for the
// WMO statistical product templates that carry one (4.8–4.14). ok=false for
// every other template.
//
// 为什么需要它：统计模板把预报期拆在两地 —— 表 4.4 时间字段是"区间起点"，
// end-of-interval 时间戳是"区间终点"。ecCodes 的 forecastTime/step/
// validityDate 就必须同时读这两处（step = 终点 − 起报时间，validityDate =
// 终点）。只解析第一个 4.4 字段的解析器会在每一条累积/平均产品上给出
// step=0、时效偏早的结果。
//
// Why it exists: statistical templates split the forecast period over two
// locations — the table 4.4 time field is the interval *start*, the
// end-of-interval timestamp is the interval *end*. ecCodes' forecastTime /
// step / validityDate are only reproducible by reading both (step = end −
// reference time, validityDate = end). A parser that stops at the first 4.4
// field reports step=0 and an early validity time on every accumulated or
// averaged product.
//
// id 是 "4.8" 这类模板 id（与 Template().ID 一致）。
//
// id is a template id ("4.8"), matching Template().ID.
func EndIntervalOffset(id string) (int, bool) {
	off, exists := genEndIntervalOffsets[id]
	if !exists || off < 0 {
		return 0, false
	}
	return int(off), true
}

// EndIntervalParametrized reports whether a template carries an
// "...end of overall time interval" timestamp whose position cannot be bound
// statically (it sits inside a parametrized repetition, e.g. templates 4.34,
// 4.114, 4.116). endIntervalOffset reports not-ok for those ids, so this is
// how a caller tells "no such timestamp here" apart from "timestamp present
// but unreachable" — the message needs per-instance parsing that this
// spec-level data deliberately does not provide.
func EndIntervalParametrized(id string) bool {
	return genEndIntervalOffsets[id] < 0
}

// StatProcessOffset returns the 0-based offset of the typeOfStatisticalProcessing
// byte (table 4.7) of a product template's outermost time-range specification —
// the anchor for the ecCodes "typeOfStatisticalProcessing" key, which ~2000
// entries of the parameter concepts condition on. ok=false for templates
// without such a field at a fixed position (instantaneous templates and the
// parametrized-position ones).
func StatProcessOffset(id string) (int, bool) {
	// 4.112/4.123 的 statisticalProcessesList 从八位组 47 开始，只有后续
	// 元素随 NT 平移；第一个元素的位置仍是固定的。生成器因为模板写作
	// "47+(NT-1)*12" 而无法静态求值，这里补上。
	switch id {
	case "4.112", "4.123":
		return 46, true
	}
	off, ok := genStatProcessOffsets[id]
	return int(off), ok
}

// statisticalRecordSize 是统计模板里单个 time-range 记录的字节数
// （template.4.statistical.def：statistical process 1 + type of time
// increment 1 + length of time range 4 + its unit 1 + time increment 4），
// 族内全部模板一致 —— 已用 ecCodes 2.47 逐模板实测 NT=1 与 NT=3 的偏移差
// 恒为 24 验证。
const statisticalRecordSize = 12

// NumTimeRangesOffset returns the 0-based section offset of
// numberOfTimeRanges ("NT") in the statistical time-range block.
//
// 每个带 template.4.statistical.def 的模板都是同一字段顺序：区间终点时间戳
// 7 字节（年 2 + 月/日/时/分/秒 各 1），紧跟 NT，再 4 字节缺失计数，然后 NT
// 条 12 字节记录。所以 NT 位置 = 终点时间戳起点 + 7。
//
// Every template that includes template.4.statistical.def orders its fields the
// same way: the 7-byte end-of-overall-time-interval timestamp, then NT, then
// four bytes of numberOfMissingInStatisticalProcess, then NT 12-byte records.
// ok=false when the timestamp itself sits at a parametrized position (4.34,
// 4.114, 4.116, …), because then nothing downstream can be bound statically.
func NumTimeRangesOffset(id string) (int, bool) {
	off, ok := EndIntervalOffset(id)
	if !ok {
		return 0, false
	}
	return off + 7, true
}

// StatProcessOffsetFor returns the 0-based offset of the table 4.7 "Statistical
// process" byte that ecCodes exposes as the scalar key
// typeOfStatisticalProcessing.
//
// 语义按 ecCodes 实测：重复块里的标量键映射到**最后一条**记录 —— 把 NT=3 的
// 三条记录分别改成 11/22/33 后，ecCodes 的 typeOfStatisticalProcessing 读出
// 33（codes_get_array 才是 [11 22 33]）。因此有 NT 条记录时偏移要前进
// (NT-1)*12。NT 缺失、为 1、或末条记录越界时退回首条记录的位置。
//
// Semantics follow ecCodes: a scalar key over a repeated block maps to the LAST
// record (with NT=3 records patched to 11/22/33, ecCodes reads 33; only
// codes_get_array reports all three). So N records advance the byte by
// (N-1)*12. Falls back to the first record when NT is missing, 1, or the last
// record would run past the section data.
func StatProcessOffsetFor(id string, data []byte) (int, bool) {
	first, ok := StatProcessOffset(id)
	if !ok {
		return 0, false
	}
	ntOff, ok := NumTimeRangesOffset(id)
	if !ok || ntOff < 0 || ntOff >= len(data) {
		return first, true
	}
	nt := int(data[ntOff])
	if nt <= 1 {
		return first, true
	}
	last := first + (nt-1)*statisticalRecordSize
	if last >= len(data) {
		return first, true
	}
	return last, true
}

// ProbTypeOffset returns the 0-based offset of the "Probability type" byte
// (table 4.9) of a product template — the anchor for the ecCodes
// "probabilityType" key, which the probability concepts (tpg1, cdsn1, …)
// condition on. The lower limit's scale factor / scaled value follow at
// +1/+2 and the upper limit's at +6/+7 in every template that carries the
// field at a fixed position. ok=false for non-probability templates and for
// the parametrized-position ones (4.112/4.123).
func ProbTypeOffset(id string) (int, bool) {
	off, ok := genProbTypeOffsets[id]
	return int(off), ok
}

// ProbTypeOffsetParametrized returns the 0-based offset of the "Probability
// type" byte for the two templates whose probability block follows the
// statisticalProcessesList and therefore shifts with the number of time-range
// specifications NT (section 4 octet 42, 1-based):
//
//	4.112: octet 61 + (NT-1)*12
//	4.123: octet 66 + (NT-1)*12
//
// The lower/upper limit layout after the type byte is identical to the
// fixed-position probability templates (+1/+2..+5 and +6/+7..+10).
//
// ok=false for every other template, for a missing/zero NT, and when the
// computed block would run past data.
func ProbTypeOffsetParametrized(id string, data []byte) (int, bool) {
	var base int // 1-based octet of probabilityType when NT == 1
	switch id {
	case "4.112":
		base = 61
	case "4.123":
		base = 66
	default:
		return 0, false
	}
	if len(data) < 42 {
		return 0, false
	}
	nt := int(data[41])
	if nt < 1 {
		return 0, false
	}
	off := base - 1 + (nt-1)*12
	if off+11 > len(data) {
		return 0, false
	}
	return off, true
}
