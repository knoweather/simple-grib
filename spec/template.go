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
