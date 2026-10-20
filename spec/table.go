package spec

import "fmt"

// Entry 表示代码表的一行：落在 Min..Max 区间内的所有代码共用同一个 Name。
//
// Entry is one row of a code table: every code inside the Min..Max range
// shares the same Name.
type Entry struct {
	Min    int
	Max    int
	Name   string
	Status string // 条目状态："Operational" / "Experimental" / entry status
	// Unit 是该表在 UnitComments_en 列给出的单位（如 K、Pa、m）。没有单位列、
	// 空白、"-"（WMO 的无量纲写法）或指向别表的注释都归一成 ""。取到空串
	// 只表示"该表没有声明单位"，不表示"无量纲"。
	//
	// Unit is the unit this table declares in its UnitComments_en column (K,
	// Pa, m, …). Missing column, blank, "-" (WMO's dimensionless spelling)
	// and cross-table notes all normalize to "". An empty Unit means "this
	// table declares no unit", not "dimensionless".
	Unit string
}

// Table 是一组带名字的条目集合，例如 "4.1"（参数类别）或 "CCT-11"（中心）。
//
// Table is a named collection of entries, for example "4.1" (parameter
// categories) or "CCT-11" (reporting centres).
type Table struct {
	ID      string
	Title   string
	Entries []Entry
}

// tablesByID 按表 id 索引全部表，由 init() 从生成的 genTables 切片构建。
//
// tablesByID indexes every table by id; init builds it from the generated
// genTables slice.
var tablesByID = map[string]*Table{}

func init() {
	for _, gt := range genTables {
		t := &Table{ID: gt.id, Title: gt.title}
		for _, e := range gt.entries {
			t.Entries = append(t.Entries, Entry{Min: int(e.min), Max: int(e.max), Name: e.name, Status: e.status, Unit: e.unit})
		}
		tablesByID[t.ID] = t
	}
}

// TableByID 返回指定 id 的表（"4.1"、"4.2.0.13"、"CCT-11" …）。返回的 Table
// 指向包内共享的表数据，调用方必须按只读使用。
//
// TableByID returns the table with the given id ("4.1", "4.2.0.13",
// "CCT-11", …). The returned Table points at package-shared table data and
// must be treated as read-only.
func TableByID(id string) (*Table, bool) {
	t, ok := tablesByID[id]
	return t, ok
}

// Lookup 查找 [Min,Max] 区间包含 code 的条目。交叉引用行（Min>Max 的条目，
// 例如 "See Common code table C-14" 这类指向其他表的文本）永不匹配；需要该
// 引用文本时读取 Table.Entries[0].Name。
//
// Lookup finds the entry whose [Min,Max] range contains code. Cross-reference
// rows (entries with Min>Max, such as "See Common code table C-14") never
// match; read Table.Entries[0].Name to obtain the reference text.
func (t *Table) Lookup(code int) (Entry, bool) {
	for _, e := range t.Entries {
		if code >= e.Min && code <= e.Max {
			return e, true
		}
	}
	return Entry{}, false
}

// ParameterName 把 WMO 参数三元组 (discipline, category, number) 解析成参数名：
// 先查层级表 4.2.<discipline>.<category>，未命中时回退到扁平表 "4.2"；两张表
// 都查不到就返回错误。
//
// ParameterName resolves a WMO parameter triple (discipline, category,
// number) to its name: it first consults the hierarchical table
// 4.2.<discipline>.<category> and falls back to the flat "4.2" table on a
// miss; if neither table matches it returns an error.
func ParameterName(discipline, category, number int) (string, error) {
	id := fmt.Sprintf("4.2.%d.%d", discipline, category)
	if t, ok := tablesByID[id]; ok {
		if e, ok := t.Lookup(number); ok {
			return e.Name, nil
		}
	}
	if t, ok := tablesByID["4.2"]; ok {
		if e, ok := t.Lookup(number); ok {
			return e.Name, nil
		}
	}
	return "", fmt.Errorf("spec: no parameter %d.%d found in table 4.2", discipline, number)
}

// ParameterUnit resolves a WMO parameter triple (discipline, category,
// number) to the unit declared for it: it first consults the hierarchical
// table 4.2.<discipline>.<category> and falls back to the flat "4.2" table
// on a miss, mirroring ParameterName's lookup order. An empty unit with a
// nil error means the table exists and matches, but declares no unit for
// that parameter (WMO table 4.2's local and reserved codes land here, as do
// the dimensionless ones spelled "-"); use a units error only to tell "no
// parameter table at all" apart from "parameter known, unit not declared".
//
// 单位取自 WMO 表的 UnitComments_en 列原文，保持 WMO 拼写（"W m-2"、
// "m s-1"、"kg/kg"），不做 ecCodes 式归一 —— 需要归一化的调用方自己映射。
//
// Units are returned verbatim from the WMO tables' UnitComments_en column
// ("W m-2", "m s-1", "kg/kg"), not normalized the way ecCodes spells them
// ("W m**-2" variants differ across the WMO files themselves); callers that
// need normalization must do it themselves.
func ParameterUnit(discipline, category, number int) (string, error) {
	id := fmt.Sprintf("4.2.%d.%d", discipline, category)
	if t, ok := tablesByID[id]; ok {
		if e, ok := t.Lookup(number); ok {
			return e.Unit, nil
		}
	}
	if t, ok := tablesByID["4.2"]; ok {
		if e, ok := t.Lookup(number); ok {
			return e.Unit, nil
		}
	}
	return "", fmt.Errorf("spec: no parameter %d.%d found in table 4.2", discipline, number)
}

// LevelUnit returns the unit declared for a table 4.5 fixed-surface type
// ("Pa" for isobaric surfaces, "m" for height above ground, "layer" for
// layer types). WMO misses render the unit as "-", so local/unallocated
// types return an empty unit with ok=false.
func LevelUnit(surfaceType int) (string, bool) {
	t, ok := tablesByID["4.5"]
	if !ok {
		return "", false
	}
	e, ok := t.Lookup(surfaceType)
	if !ok || e.Unit == "" {
		return "", false
	}
	return e.Unit, true
}
