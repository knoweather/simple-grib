package spec

import "testing"

// 本文件钉住 ParameterNameFor 的语义：ecCodes 的 parameterName 是「报文自己声明的
// 表版本」下 4.2 表的标题文本（Codetable.cc 的 codetable_title）。
//
// 期望值全部来自 ecCodes 的现场测量（oracle），不是从本仓库的数据反推的。做法是
// 取 testdata/ecc-xcheck/poscorners.grib2 造一条消息，改写 tablesVersion /
// localTablesVersion / centre / discipline / parameterCategory / parameterNumber，
// 再读 parameterName。
//
// 测量的基线是 ecCodes 2.47.0（tablesVersionLatest = 36）；本模块内嵌的是 2.50 的
// definitions（最新 37），版本钳制的用例取两者都成立的边界。

func TestParameterNameForOracleCases(t *testing.T) {
	// 期望值来自 ecCodes 2.47.0 的现场测量。
	cases := []struct {
		label               string
		version, local, ctr int
		disc, cat, num      int
		want                string
		oracle              string // 实测命令（注释形式记录在 label 里）
	}{
		{"v4 (0,0,7) 标题内含括号", 4, 0, 38, 0, 0, 7, "Dew point depression (or deficit)", ""},
		{"v29 (0,0,7) 标题内含括号（措辞变了）", 29, 0, 38, 0, 0, 7, "Dewpoint depression (or deficit)", ""},
		{"v29 (2,0,0) discipline 选目录", 29, 0, 98, 2, 0, 0, "Land cover (0 = sea, 1 = land)", ""},
		{"v29 (2,0,192) ecmf 本地表补空", 29, 1, 98, 2, 0, 192, "Carbon Dioxide Net Ecosystem Exchange", ""},
		{"v29 (2,0,192) 无本地表则退化", 29, 0, 98, 2, 0, 192, "192", ""},
		{"v4 (0,0,0)", 4, 0, 38, 0, 0, 0, "Temperature", ""},
		{"v1 (0,0,0)", 1, 0, 98, 0, 0, 0, "Temperature", ""},
		{"v0 无 tables/0 目录", 0, 0, 98, 0, 0, 0, "0", ""},
		{"v4 (0,16,192) 该版无此码", 4, 0, 38, 0, 16, 192, "192", ""},
		{"v19 (0,16,192) edzw 本地表", 19, 1, 78, 0, 16, 192, "precipitation, qualified,BRD", ""},
		{"v19 (0,16,192) localTablesVersion=254 → 不用本地表", 19, 254, 78, 0, 16, 192, "192", ""},
		{"v19 (0,0,0) edzw 本地表不改 master", 19, 1, 78, 0, 0, 0, "Temperature", ""},
		{"v19 (0,0,19) 有码（本地表不覆盖已有的码）", 19, 1, 78, 0, 0, 19, "Turbulent transfer coefficient for heat", ""},
		{"v29 (0,9,0) 该版无 4.2.0.9.table", 29, 0, 98, 0, 9, 0, "0", ""},
	}
	for _, c := range cases {
		got := ParameterNameFor(ParamNameRef{
			Discipline: c.disc, Category: c.cat, Number: c.num,
			MasterVersion: c.version, LocalVersion: c.local, Centre: c.ctr,
		})
		if got != c.want {
			t.Errorf("%s: ParameterNameFor(v=%d lv=%d centre=%d d=%d c=%d n=%d) = %q, 想要 %q",
				c.label, c.version, c.local, c.ctr, c.disc, c.cat, c.num, got, c.want)
		}
	}
}

// TestParameterNameForVersionSelection 钉住 按消息表版本取题名：同一三元组
// 在报文声明的不同表版本下得到不同措辞（甚至「该版无此码 → 十进制码」）。
//
// 语料报文的取值（CMA #102/#122/#134/#128，ecCodes 实测）：
//
//	| (disc,cat,num) | v2 | v4 | v29 | v36 |
//	| (0,0,6)        | Dew point temperature | Dew point temperature | Dewpoint temperature | Dewpoint temperature |
//	| (0,1,9)        | Large scale precipitation (non-convective) | 同左 | Large-scale precipitation (non-convective) | 同左 |
//	| (0,7,13)       | 13 | 13 | Showalter index | Showalter index |
//	| (0,16,224)     | 224 | 224 | 224 | 224 |
func TestParameterNameForVersionSelection(t *testing.T) {
	cases := []struct {
		disc, cat, num int
		want           map[int]string
	}{
		{0, 0, 6, map[int]string{
			2: "Dew point temperature", 4: "Dew point temperature",
			5: "Dew point temperature", 29: "Dewpoint temperature", 36: "Dewpoint temperature",
		}},
		{0, 1, 9, map[int]string{
			2:  "Large scale precipitation (non-convective)",
			4:  "Large scale precipitation (non-convective)",
			29: "Large-scale precipitation (non-convective)",
			36: "Large-scale precipitation (non-convective)",
		}},
		{0, 7, 13, map[int]string{2: "13", 4: "13", 29: "Showalter index", 36: "Showalter index"}},
		{0, 16, 224, map[int]string{2: "224", 4: "224", 29: "224", 36: "224"}},
	}
	for _, c := range cases {
		for v, want := range c.want {
			got := ParameterNameFor(ParamNameRef{
				Discipline: c.disc, Category: c.cat, Number: c.num, MasterVersion: v,
			})
			if got != want {
				t.Errorf("v=%d (d=%d c=%d n=%d) = %q, 想要 %q", v, c.disc, c.cat, c.num, got, want)
			}
		}
	}

	// 「保留给本地使用」区间不是 ecCodes 4.2 表里的条目：simple-grib 的
	// spec.ParameterName（最新 WMO CSV，扁平回退）给 "Reserved for local use"，
	// ecCodes 的 parameterName 给十进制码。两者语义不同，各自钉住。
	if name, err := ParameterName(0, 16, 224); err != nil || name != "Reserved for local use" {
		t.Errorf("ParameterName(0,16,224) = %q, %v; 想要 Reserved for local use（CSV 区间条目）", name, err)
	}
	if got := ParameterNameFor(ParamNameRef{Discipline: 0, Category: 16, Number: 224, MasterVersion: 37}); got != "224" {
		t.Errorf("ParameterNameFor(v37 0,16,224) = %q, 想要 \"224\"（ecCodes 表里没有 192-254 条目）", got)
	}
}

// TestParameterNameForClamp 钉住版本钳制：声明版本大于内嵌最新版本时按
// pnameLatestVersion 查表（section.1.def 的 `if (tablesVersion > tablesVersionLatest)`），
// 而不是退化成十进制码。
func TestParameterNameForClamp(t *testing.T) {
	want := "Dewpoint temperature" // v36/v37 的 (0,0,6)
	for _, v := range []int{37, 38, 99, 255} {
		if got := ParameterNameFor(ParamNameRef{Number: 6, MasterVersion: v}); got != want {
			t.Errorf("ParameterNameFor(v=%d, (0,0,6)) = %q, 想要 %q（钳制到 %d）",
				v, got, want, pnameLatestVersion)
		}
	}
	if pnameLatestVersion != 37 {
		t.Errorf("pnameLatestVersion = %d, 想要 37（ecCodes 2.50.0 的 boot.def: tablesVersionLatest）", pnameLatestVersion)
	}
	// 钳制到最新 ≠ 回退到十进制码：v5 与 v255 必须不同。
	if got := ParameterNameFor(ParamNameRef{Number: 6, MasterVersion: 5}); got != "Dew point temperature" {
		t.Errorf("ParameterNameFor(v=5, (0,0,6)) = %q, 想要 Dew point temperature", got)
	}
}

// TestParameterNameForLocalTables 钉住本地表的三个开关条件（ecCodes section.1.def）：
// localTablesVersion ∈ {0,255} 时不用本地表；中心在 c-11 里没有助记符、或该中心
// 没有对应目录/文件时也等于没有本地表。
func TestParameterNameForLocalTables(t *testing.T) {
	const (
		edzw = 78
		ecmf = 98
		kwbc = 7
		babj = 38
	)
	// edzw/local/1/4.2.0.16.table 有 192；DWD 报文声明的 localTablesVersion=1。
	base := ParamNameRef{Discipline: 0, Category: 16, Number: 192, MasterVersion: 19}
	for _, c := range []struct {
		label string
		ref   ParamNameRef
		want  string
	}{
		{"edzw lv=1 命中本地表", ParamNameRef{MasterVersion: 19, Discipline: 0, Category: 16, Number: 192, LocalVersion: 1, Centre: edzw}, "precipitation, qualified,BRD"},
		{"lv=0 不用本地表", ParamNameRef{MasterVersion: 19, Discipline: 0, Category: 16, Number: 192, LocalVersion: 0, Centre: edzw}, "192"},
		{"lv=255 不用本地表", ParamNameRef{MasterVersion: 19, Discipline: 0, Category: 16, Number: 192, LocalVersion: 255, Centre: edzw}, "192"},
		{"centre 无本地目录", ParamNameRef{MasterVersion: 19, Discipline: 0, Category: 16, Number: 192, LocalVersion: 1, Centre: babj}, "192"},
		{"centre=0（WMO 秘书处）等价于无本地表", ParamNameRef{MasterVersion: 19, Discipline: 0, Category: 16, Number: 192, LocalVersion: 1, Centre: 0}, "192"},
		{"ecmf lv=1 的 4.2.2.0.table 补 discipline 2", ParamNameRef{MasterVersion: 29, Discipline: 2, Category: 0, Number: 192, LocalVersion: 1, Centre: ecmf}, "Carbon Dioxide Net Ecosystem Exchange"},
		{"ecmf lv=1 但版本 1 无该本地文件→仍需 master 命中", ParamNameRef{MasterVersion: 1, Discipline: 0, Category: 0, Number: 0, LocalVersion: 1, Centre: ecmf}, "Temperature"},
		{"kwbc/local/1 没有 4.2.0.1.table", ParamNameRef{MasterVersion: 29, Discipline: 0, Category: 1, Number: 192, LocalVersion: 1, Centre: kwbc}, "192"},
	} {
		got := ParameterNameFor(c.ref)
		if got != c.want {
			t.Errorf("%s: = %q, 想要 %q", c.label, got, c.want)
		}
	}
	_ = base
}

// TestParameterNameForNumericFallback 钉住「查不到就是十进制码」这条兜底，
// 与 ecCodes CodetableTitle::unpack_string 的 snprintf("%d", value) 一致。
func TestParameterNameForNumericFallback(t *testing.T) {
	for _, c := range []struct {
		label string
		ref   ParamNameRef
		want  string
	}{
		{"版本 0 没有目录", ParamNameRef{MasterVersion: 0}, "0"},
		{"不存在的表文件", ParamNameRef{MasterVersion: 29, Category: 9}, "0"},
		// 255 在 4.2 表里是 "Missing"（占位条目），不是数字兜底 ——
		// ecCodes: v19 (0,16,255) => 'Missing'。
		{"码 255 是表里的 Missing 条目", ParamNameRef{MasterVersion: 19, Discipline: 0, Category: 16, Number: 255}, "Missing"},
		{"discipline 不在任何表里", ParamNameRef{MasterVersion: 29, Discipline: 250, Category: 0, Number: 7}, "7"},
		{"category 不在任何表里", ParamNameRef{MasterVersion: 29, Discipline: 0, Category: 250, Number: 7}, "7"},
		{"负版本（未知）", ParamNameRef{MasterVersion: -1, Discipline: 0, Category: 0, Number: 7}, "7"},
		// localVersion 有值但没有对应本地目录/文件时，仍然按主表回答（ecCodes 的
		// grib_context_full_defs_path 找不到文件就返回 NULL，不参与装载）。
		{"本地版本无目录→仍用主表", ParamNameRef{MasterVersion: 29, Discipline: 0, Category: 0, Number: 6, LocalVersion: 254, Centre: 78}, "Dewpoint temperature"},
	} {
		got := ParameterNameFor(c.ref)
		if got != c.want {
			t.Errorf("%s: = %q, 想要 %q", c.label, got, c.want)
		}
	}
	// ecCodes 的 parameterNumber 是 1 字节的码表值，任何取值都必须有非空结果。
	for n := 0; n < 256; n++ {
		if got := ParameterNameFor(ParamNameRef{MasterVersion: 4, Discipline: 0, Category: 0, Number: n}); got == "" {
			t.Fatalf("ParameterNameFor(n=%d) 为空；ecCodes 至少会退化成十进制码", n)
		}
	}
}

// TestParameterNameLookupIsDedupedAndSorted 是生成数据的结构不变量：每张池内表都
// 严格按 code 升序（二分查找的前提），且 (version, discipline, category) 索引指向
// 合法池下标。
func TestParameterNameLookupIsDedupedAndSorted(t *testing.T) {
	for i, tbl := range pnameTables {
		for j := 1; j < len(tbl); j++ {
			if tbl[j-1].code >= tbl[j].code {
				t.Fatalf("pnameTables[%d] 未严格升序：%d >= %d", i, tbl[j-1].code, tbl[j].code)
			}
		}
	}
	for k, idx := range pnameMasterIndex {
		if idx < 0 || int(idx) >= len(pnameTables) {
			t.Fatalf("pnameMasterIndex[0x%06x] = %d 越界", k, idx)
		}
	}
	for k, idx := range pnameLocalIndex {
		if idx < 0 || int(idx) >= len(pnameTables) {
			t.Fatalf("pnameLocalIndex[0x%012x] = %d 越界", k, idx)
		}
	}
	// 内嵌数据覆盖 1..pnameLatestVersion 每个版本（至少 4.2.0.0.table 存在）。
	for v := 1; v <= pnameLatestVersion; v++ {
		if _, ok := pnameMasterIndex[uint32(v)<<16|0<<8|0]; !ok {
			t.Errorf("版本 %d 的 4.2.0.0.table 没有内嵌", v)
		}
	}
}
