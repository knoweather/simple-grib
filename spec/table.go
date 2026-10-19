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
			t.Entries = append(t.Entries, Entry{Min: int(e.min), Max: int(e.max), Name: e.name, Status: e.status})
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
