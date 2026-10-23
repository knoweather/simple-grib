// paramname.go 生成 spec/paramname_gen.go：按消息声明的表版本选取 WMO 表 4.2
// 的表题名（ecCodes 的 parameterName）。
//
// 输入（第二个生成输入源，见 -paramdata）：
//
//	paramdata/grib2/tables/<v>/4.2.<discipline>.<category>.table   （v = 1..37）
//	paramdata/grib2/tables/local/<centre>/<lv>/4.2.<d>.<c>.table
//	paramdata/common/c-11.table                                    （中心号 → 助记符）
//
// 这些 .table 文件是 ecCodes definitions 的原样副本，行格式与
// src/eccodes/accessor/Codetable.cc 的 grib_load_codetable() 一致：
//
//	<code> <abbreviation> <title> (<units>)
//
// 只有 title 参与 parameterName（CodetableTitle::unpack_string 取
// entries[code].title）；units 与 abbreviation 本生成器丢弃。
//
// 为什么需要 c-11.table：ecCodes 的本地表目录名不是中心号，而是
// common/c-11.table 里该中心的助记符 —— section.1.def 写的是
//
//	transient localDir="grib2/tables/local/[centre]/[localTablesVersion]";
//
// 而 `centre` 这个 codetable 带 string_type，`[centre]` 因此解析成助记符
// （"ecmf"、"edzw"、"kwbc"、…），不是数字。本生成器用 c-11.table 把目录名换回
// 中心号，从而在运行时只需中心号即可定位本地表。
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// pnameFileNameRe 匹配 ecCodes 能实际选中的表名：
// template.4.parameter.def 的 tablename 恒为
// "4.2.[discipline:l].[parameterCategory:l].table"。paramdata 里形如
// 4.201.table / 4.230.table 的文件（三段或更少）永远不可能被
// parameterName 选中，本生成器只统计不展开。
var pnameFileNameRe = regexp.MustCompile(`^4\.2\.(\d+)\.(\d+)\.table$`)

// pnameAllRe 匹配 grib2/tables 下全部 "4.2*.table" 候选文件（含上面的不可达者）。
var pnameAllRe = regexp.MustCompile(`^4\.2.*\.table$`)

// pnameRow 是一行生成索引：某张 .table 文件在去重池里的位置。
type pnameRow struct {
	master int // master 版本号（master 行）或本地版本号（local 行）
	centre int // 0 = master 行；否则为中心号
	disc   int
	cat    int
	pool   int
	file   string // 相对 paramdata 的路径，写进生成的注释
}

type pnameSet struct {
	tables   [][]pnameEntry
	poolIdx  map[string]int
	master   []pnameRow
	local    []pnameRow
	latest   int
	total    int            // 见到的 4.2*.table 文件总数（含不可达的）
	skipped  int            // 不可达（非 4.2.<d>.<c>.table，如 4.201.table）的文件数
	orphan   int            // 本地表目录在 c-11.table 里没有助记符（ecCodes 不可达）
	unmapped []string       // 本地表目录名在 c-11.table 里找不到中心号
	reused   map[int]string // 池条目 → 首次出现的相对路径（注释用）
	shared   map[int]int    // 池条目 → 引用它的文件数
}

type pnameEntry struct {
	code  int
	title string
}

// pnameEntryLess 按 code 升序（生成的池里每个表都严格升序，运行时可二分）。
func (s *pnameSet) add(tbl []pnameEntry, rel string, row pnameRow) {
	var key bytes.Buffer
	for _, e := range tbl {
		fmt.Fprintf(&key, "%d\x00%s\x01", e.code, e.title)
	}
	k := key.String()
	idx, ok := s.poolIdx[k]
	if !ok {
		idx = len(s.tables)
		s.poolIdx[k] = idx
		s.tables = append(s.tables, tbl)
		s.reused[idx] = rel
	}
	s.shared[idx]++
	row.pool = idx
	row.file = rel
	if row.centre == 0 {
		s.master = append(s.master, row)
	} else {
		s.local = append(s.local, row)
	}
}

func genParamName(dir string) (*pnameSet, error) {
	tablesDir := filepath.Join(dir, "grib2", "tables")
	entries, err := os.ReadDir(tablesDir)
	if err != nil {
		return nil, fmt.Errorf("paramname: %w", err)
	}
	set := &pnameSet{poolIdx: map[string]int{}, reused: map[int]string{}, shared: map[int]int{}}

	// 版本目录 1..N，按数字升序。ecCodes 的 boot.def 把
	// masterTablesVersionNumber 钳制到 constant tablesVersionLatest；对
	// vendored 的 ecCodes 2.50.0 定义它是 37，恰好等于这里最大的版本目录。
	var versions []int
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "local" {
			continue
		}
		v, err := strconv.Atoi(e.Name())
		if err != nil || v < 0 || v > 255 {
			return nil, fmt.Errorf("paramname: unexpected directory %q under %s (want a numeric tables version)", e.Name(), tablesDir)
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)
	if len(versions) == 0 {
		return nil, fmt.Errorf("paramname: no version directories under %s", tablesDir)
	}
	set.latest = versions[len(versions)-1]

	for _, v := range versions {
		vdir := filepath.Join(tablesDir, strconv.Itoa(v))
		files, err := os.ReadDir(vdir)
		if err != nil {
			return nil, fmt.Errorf("paramname: %w", err)
		}
		var names []string
		for _, f := range files {
			if f.IsDir() || !pnameAllRe.MatchString(f.Name()) {
				continue
			}
			names = append(names, f.Name())
		}
		sort.Strings(names)
		for _, name := range names {
			set.total++
			m := pnameFileNameRe.FindStringSubmatch(name)
			if m == nil {
				set.skipped++
				continue
			}
			disc, _ := strconv.Atoi(m[1])
			cat, _ := strconv.Atoi(m[2])
			if disc > 255 || cat > 255 {
				return nil, fmt.Errorf("paramname: %s: discipline/category out of the 1-byte range", name)
			}
			rel := filepath.ToSlash(filepath.Join("grib2", "tables", strconv.Itoa(v), name))
			tbl, err := parsePnameTable(filepath.Join(vdir, name), rel)
			if err != nil {
				return nil, err
			}
			set.add(tbl, rel, pnameRow{master: v, disc: disc, cat: cat})
		}
	}

	// 本地表：local/<助记符>/<本地版本>/4.2.<d>.<c>.table。
	abbrevCodes, err := parseC11CentreAbbrev(filepath.Join(dir, "common", "c-11.table"))
	if err != nil {
		return nil, err
	}
	localRoot := filepath.Join(tablesDir, "local")
	centres, err := os.ReadDir(localRoot)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("paramname: %w", err)
	}
	var centreNames []string
	for _, c := range centres {
		if c.IsDir() {
			centreNames = append(centreNames, c.Name())
		}
	}
	sort.Strings(centreNames)
	for _, centre := range centreNames {
		codes, ok := abbrevCodes[centre]
		if !ok {
			// ecCodes 只能通过 centre 的助记符选中本地目录；c-11.table 里没有
			// 这个助记符，说明任何 GRIB2 消息都走不到它（例如 era6，它由
			// section 4 的 datasetForLocal 概念选择，而不是 centre）。
			set.unmapped = append(set.unmapped, centre)
			// 只为统计口径完整（total/orphan）：这些文件没有任何消息能选中。
			_ = filepath.WalkDir(filepath.Join(localRoot, centre), func(p string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() && pnameAllRe.MatchString(d.Name()) {
					set.total++
					set.orphan++
				}
				return nil
			})
			continue
		}
		croot := filepath.Join(localRoot, centre)
		lvDirs, err := os.ReadDir(croot)
		if err != nil {
			return nil, fmt.Errorf("paramname: %w", err)
		}
		var lvs []int
		for _, d := range lvDirs {
			if !d.IsDir() {
				continue
			}
			lv, err := strconv.Atoi(d.Name())
			if err != nil || lv < 1 || lv > 254 {
				return nil, fmt.Errorf("paramname: %s/%s: unexpected local tables version directory", centre, d.Name())
			}
			lvs = append(lvs, lv)
		}
		sort.Ints(lvs)
		for _, lv := range lvs {
			files, err := os.ReadDir(filepath.Join(croot, strconv.Itoa(lv)))
			if err != nil {
				return nil, fmt.Errorf("paramname: %w", err)
			}
			var names []string
			for _, f := range files {
				if f.IsDir() || !pnameAllRe.MatchString(f.Name()) {
					continue
				}
				names = append(names, f.Name())
			}
			sort.Strings(names)
			for _, name := range names {
				set.total++
				m := pnameFileNameRe.FindStringSubmatch(name)
				if m == nil {
					set.skipped++
					continue
				}
				disc, _ := strconv.Atoi(m[1])
				cat, _ := strconv.Atoi(m[2])
				if disc > 255 || cat > 255 {
					return nil, fmt.Errorf("paramname: %s: discipline/category out of the 1-byte range", name)
				}
				rel := filepath.ToSlash(filepath.Join("grib2", "tables", "local", centre, strconv.Itoa(lv), name))
				tbl, err := parsePnameTable(filepath.Join(croot, strconv.Itoa(lv), name), rel)
				if err != nil {
					return nil, err
				}
				for _, code := range codes {
					set.add(tbl, rel, pnameRow{master: lv, centre: code, disc: disc, cat: cat})
				}
			}
		}
	}
	return set, nil
}

// parsePnameTable 逐个读出一张 4.2 表，严格照 Codetable.cc 的
// grib_load_codetable() 解析：
//
//   - "#" 开头（允许前导空白）的行与空行跳过；
//   - 行首非数字 → ecCodes 只记一条错误日志并跳过该行，这里直接报错（生成器
//     的纪律是"失败即报错"，不猜一个表内容）；
//   - 第一个空白前的数字是 code；第二个 token 是助记符（丢弃）；
//   - 标题是剩余文本一直到**整行最后一个 '('**（若该括号在标题起点之后），
//     再去掉尾部空白 —— 与 ecCodes 的 strrchr(line,'(') + string_rtrim 一致；
//   - 同一 code 重复出现时保留**第一条**（ecCodes 对已填过的槽位直接 continue）。
func parsePnameTable(path, rel string) ([]pnameEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("paramname: %w", err)
	}
	byCode := map[int]string{}
	for lineNo, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		rest := strings.TrimLeft(line, " \t\v\f\r")
		if rest == "" || strings.HasPrefix(rest, "#") {
			continue
		}
		if rest[0] < '0' || rest[0] > '9' {
			return nil, fmt.Errorf("paramname: %s:%d: line does not start with a code: %q", rel, lineNo+1, rest)
		}
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		code, err := strconv.Atoi(rest[:i])
		if err != nil || code > 255 {
			return nil, fmt.Errorf("paramname: %s:%d: code %q out of the 1-byte range", rel, lineNo+1, rest[:i])
		}
		if i < len(rest) && !isSpaceByte(rest[i]) {
			// ecCodes 会把 "12a" 当成 code=12*10+('a'-'0') 这种垃圾值；拒绝。
			return nil, fmt.Errorf("paramname: %s:%d: code is not followed by whitespace: %q", rel, lineNo+1, rest)
		}
		j := i
		for j < len(rest) && isSpaceByte(rest[j]) {
			j++
		}
		k := j
		for k < len(rest) && !isSpaceByte(rest[k]) {
			k++
		}
		if k == j {
			return nil, fmt.Errorf("paramname: %s:%d: no abbreviation after code %d", rel, lineNo+1, code)
		}
		t := k
		for t < len(rest) && isSpaceByte(rest[t]) {
			t++
		}
		// 标题终点 = 整行最后一个 '('（换算到 rest 的坐标）。
		off := len(line) - len(rest)
		titleEnd := len(rest)
		if lp := strings.LastIndexByte(line, '('); lp >= 0 && lp-off >= t {
			titleEnd = lp - off
		}
		title := strings.TrimRight(rest[t:titleEnd], " \t\v\f\r")
		if title == "" {
			return nil, fmt.Errorf("paramname: %s:%d: empty title for code %d", rel, lineNo+1, code)
		}
		if _, dup := byCode[code]; !dup {
			byCode[code] = title
		}
	}
	if len(byCode) == 0 {
		// 合法：ecCodes 自带 "# empty file" 占位表（如 tables/5/4.2.10.191.table），
		// 装载后每个码都退化到十进制码。生成一个空池条目即可。
		return []pnameEntry{}, nil
	}
	out := make([]pnameEntry, 0, len(byCode))
	for code, title := range byCode {
		out = append(out, pnameEntry{code: code, title: title})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].code < out[b].code })
	return out, nil
}

func isSpaceByte(c byte) bool { return c == ' ' || c == '\t' || c == '\v' || c == '\f' || c == '\r' }

// parseC11CentreAbbrev 解析 common/c-11.table（行格式 "<code> <mnemonic> <title>"），
// 返回助记符 → 中心号列表（同一助记符可以对应多个码：c-11.table 里
// "2 2 Melbourne (WMC)" 这类条目的助记符就是数字本身）。
func parseC11CentreAbbrev(path string) (map[string][]int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("paramname: %w", err)
	}
	out := map[string][]int{}
	for lineNo, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		rest := strings.TrimLeft(line, " \t\v\f\r")
		if rest == "" || strings.HasPrefix(rest, "#") {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 2 {
			return nil, fmt.Errorf("paramname: %s:%d: want \"<code> <mnemonic> [title]\", got %q", path, lineNo+1, rest)
		}
		code, err := strconv.Atoi(f[0])
		if err != nil || code < 0 || code > 65535 {
			return nil, fmt.Errorf("paramname: %s:%d: bad centre code %q", path, lineNo+1, f[0])
		}
		out[f[1]] = append(out[f[1]], code)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("paramname: %s: no entries", path)
	}
	return out, nil
}

// goSource 渲染 spec/paramname_gen.go 的正文（不含包声明与来源注释）。
func (s *pnameSet) goSource() string {
	var b bytes.Buffer
	b.WriteString("\n// ---- generated table 4.2 titles (ecCodes parameterName) ---------------\n\n")
	fmt.Fprintf(&b, `// pnameLatestVersion 是 vendored 表目录里的最大 master 版本号。ecCodes 把
// masterTablesVersionNumber 钳制到 boot.def 的 constant tablesVersionLatest
// （钳制发生在 section.1.def 的 masterDir 计算里），对 vendored 的 ecCodes
// 2.50.0 定义该常量是 %d，与这里的最大目录号相同。
//
// pnameLatestVersion is the highest master tables version vendored under
// paramdata/grib2/tables. ecCodes clamps masterTablesVersionNumber to
// boot.def's tablesVersionLatest (see section.1.def's masterDir), which for
// the vendored ecCodes 2.50.0 definitions is %d — the same number.
const pnameLatestVersion = %d

`, s.latest, s.latest, s.latest)
	b.WriteString(`// pnameEntry 是一张 4.2 表里的一行：code 与表题名（title）。标题即 ecCodes
// codetable_title(parameterNumber) 返回的 parameterName；行里的助记符与单位列
// 都不参与该键，本生成器丢弃它们。
type pnameEntry struct {
	code  uint8
	title string
}

`)
	fmt.Fprintf(&b, "// pnameTables 是 vendored 的 4.2 表去重池：内容完全相同的文件共用一项。\n"+
		"// 每张表按 code 严格升序，运行时可二分。共 %d 项、%d 行。\n"+
		"//\n"+
		"// pnameTables is the deduplicated pool of vendored table 4.2 files: files with\n"+
		"// identical (code, title) rows share one entry. Rows are sorted by code.\n"+
		"var pnameTables = [][]pnameEntry{\n", len(s.tables), s.totalRows())
	for i, tbl := range s.tables {
		share := ""
		if n := s.shared[i]; n > 1 {
			share = fmt.Sprintf(" (%d files)", n)
		}
		fmt.Fprintf(&b, "\t// paramdata/%s%s\n\t{\n", s.reused[i], share)
		for _, e := range tbl {
			fmt.Fprintf(&b, "\t\t{%d, %s},\n", e.code, strconv.Quote(e.title))
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n\n")

	b.WriteString(`// pnameMasterIndex 把 (masterTablesVersionNumber, discipline, parameterCategory)
// 映射到 pnameTables 的下标；key = version<<16 | discipline<<8 | category。
// 每行对应 paramdata/grib2/tables/<version>/4.2.<discipline>.<category>.table。
//
// pnameMasterIndex maps (masterTablesVersionNumber, discipline, parameterCategory)
// to a pnameTables index.
var pnameMasterIndex = map[uint32]int32{
`)
	for _, r := range s.master {
		fmt.Fprintf(&b, "\t0x%06x: %d, // paramdata/%s\n", r.master<<16|r.disc<<8|r.cat, r.pool, r.file)
	}
	b.WriteString("}\n\n")

	b.WriteString(`// pnameLocalIndex 把 (centre, localTablesVersion, discipline, parameterCategory)
// 映射到 pnameTables 的下标；key = centre<<32 | localVersion<<24 |
// discipline<<16 | category。每行对应
// paramdata/grib2/tables/local/<centre 的 c-11 助记符>/<localVersion>/4.2.<d>.<c>.table。
//
// pnameLocalIndex maps (centre, localTablesVersion, discipline, parameterCategory)
// to a pnameTables index. The centre is the numeric code from section 1 octets
// 6-7, resolved to the ecCodes local directory through common/c-11.table.
var pnameLocalIndex = map[uint64]int32{
`)
	for _, r := range s.local {
		key := uint64(r.centre)<<32 | uint64(r.master)<<24 | uint64(r.disc)<<16 | uint64(r.cat)
		fmt.Fprintf(&b, "\t0x%012x: %d, // paramdata/%s\n", key, r.pool, r.file)
	}
	b.WriteString("}\n")
	return b.String()
}

func (s *pnameSet) totalRows() int {
	n := 0
	for _, t := range s.tables {
		n += len(t)
	}
	return n
}
