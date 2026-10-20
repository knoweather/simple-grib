package paramdb

import (
	"strconv"
	"strings"

	"github.com/knoweather/simple-grib"
)

// addMarsKeys 把 MARS 标签写进查询 Map：marsClass/marsType/marsStream 与等价的
// class/type/stream 别名（ecCodes 的 mars_labeling.def 里 `alias mars.class =
// class` 等），以及 experimentVersionNumber。
//
// 键只在消息确实带 MARS 标签时才写——没有标签就不给键，引用它的概念条件自然
// 不命中，这与 ecCodes 对无标签消息的行为一致（grib_get_long 取不到 marsClass，
// 条件恒不成立）。"有没有标签"由 grib 包按 ecCodes 的本地定义分派判定，见
// grib/mars.go；这里不再自己看字节。
func addMarsKeys(q Map, m *grib.Message) {
	ml, ok := m.MarsLabelling()
	if !ok {
		return
	}
	setMarsKeys(q, ml)
}

// setMarsKeys 写出 MARS 标签键。
//
// marsClass/marsType/marsStream 是 codetable：ecCodes 概念条件写的是整数
// （grib2/section.4.def 的 datasetForLocal：`marsClass=50;`、`era6=40;`、…），
// 而 Concept.cc 依值表达式的 native_type 选比较方式——数字字面量走
// grib_get_long，所以这里必须给整数 code，不能给缩写。
//
// class/type/stream 是 g2_mars_labeling 算出的缩写视图（mars_labeling.def 的
// `alias mars.class = class`），给缩写，与 ecCodes 的字符串视图一致。
func setMarsKeys(q Map, ml *grib.MarsLabelling) {
	q["marsClass"] = strconv.Itoa(ml.Class)
	q["marsType"] = strconv.Itoa(ml.Type)
	q["marsStream"] = strconv.Itoa(ml.Stream)
	if s, ok := marsClassAbbrev[ml.Class]; ok {
		q["class"] = s
	}
	if s, ok := marsTypeAbbrev[ml.Type]; ok {
		q["type"] = s
	}
	if s, ok := marsStreamAbbrev[ml.Stream]; ok {
		q["stream"] = s
	}
	if e := marsExpver(ml.ExperimentVersionNumber); e != "" {
		q["experimentVersionNumber"] = e
	}
}

// marsExpver 返回可用的 experimentVersionNumber，不可用时返回空串。
//
// ksec1expver[4] 在 ecCodes 里是字符串键，但 GRIB1 的裸字段不保证全是可打印
// 字符（有的编码器塞二进制）。非可打印或含 NUL 时宁可不给这个键，也不要往
// 查询 Map 里放一个无法与 ecCodes 结果对齐的值。
func marsExpver(raw string) string {
	e := strings.TrimSpace(raw)
	for i := 0; i < len(e); i++ {
		if e[i] < 0x20 || e[i] > 0x7E {
			return ""
		}
	}
	return e
}
