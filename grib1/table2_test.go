package grib1

import (
	"strconv"
	"testing"
)

// 本文件钉住 GRIB1 表 2 的选表与兜底语义：Product.ParameterName 必须与 ecCodes 的
// parameterName 一致（grib1/section.1.def:50
// `meta parameterName codetable_title(indicatorOfParameter)`，表文件名
// `grib1/2.[centreForTable2:l].[table2Version:l].table`）。
//
// 期望值全部由 ecCodes 2.50.0 实测得到，方法是把真实 GRIB1 报文的 PDS 逐字节
// 打补丁（octet 4 = table2Version、5 = centre、9 = indicatorOfParameter、
// 26 = subCentre）后 codes_new_from_message 再读 parameterName。注意：同一个
// handle 上改 table2Version 不会重载码表，必须新建 handle，否则会读到旧表的陈旧值。
//
// 45 张可达表 × 全部码共 5243 行，逐字节与 ecCodes 2.50.0 一致（0 差异）。
func TestParameterNameOracleCases(t *testing.T) {
	cases := []struct {
		centre, subCentre, version, code int
		want                             string
		why                              string
	}{
		{98, 0, 128, 167, "2 metre temperature", "ECMWF 表 128（>=128，centre 段生效）"},
		{98, 0, 171, 167, "2 metre temperature anomaly", "ECMWF 表 171：同一个码在别的表里是别的含义"},
		{98, 0, 140, 80, "Wave experimental parameter 1", "ECMWF 表 140"},
		{98, 0, 140, 254, "Wave spectral peakedness", "ECMWF 表 140 的另一个码"},
		{0, 0, 1, 2, "MSL Mean sea level pressure Pa", "WMO 国际交换表 2.0.1.table"},
		{82, 0, 1, 1, "P Pressure Pa", "table2Version < 128：centre 段被忽略，走 WMO（2.82.1.table 永远选不中）"},
		{233, 0, 1, 1, "P Pressure Pa", "同上"},
		{34, 98, 171, 167, "2 metre temperature anomaly", "section.1.def:138 特例 centre != 98 && subCentre == 98 → 借 98 的表"},
		{98, 56, 171, 167, "2 metre temperature anomaly", "centre 已经是 98，subCentre 不参与"},
		{34, 0, 171, 167, "167", "centre 34 没有表 171 → 十进制码"},
		{34, 98, 1, 2, "MSL Mean sea level pressure Pa", "version < 128 时 subCentre 特例不生效，仍是 WMO"},
		{99, 0, 99, 5, "5", "整张表不存在 → 十进制码"},
		{98, 0, 140, 0, "0", "表里没有这个码 → 十进制码"},
	}
	for _, c := range cases {
		got := parameterName(c.centre, c.subCentre, c.version, c.code)
		if got != c.want {
			t.Errorf("parameterName(centre=%d sub=%d v=%d code=%d) = %q，想要 %q（%s）",
				c.centre, c.subCentre, c.version, c.code, got, c.want, c.why)
		}
	}
}

// TestRegisterTable2ReplacesBuiltin 钉住覆盖关系：调用方注册的表整张替换内置表，
// 注册表里没有的码退化成十进制码，而不是回落到内置表。
func TestRegisterTable2ReplacesBuiltin(t *testing.T) {
	const centre, version = 98, 230
	builtin := genTable2[[2]uint8{centre, version}]
	if len(builtin) == 0 {
		t.Fatalf("内置表 (%d,%d) 为空；生成数据变了吗？", centre, version)
	}
	var code int
	for c := range builtin {
		code = int(c)
		break
	}
	builtinName := builtin[uint8(code)]
	missing := 0
	for builtin[uint8(missing)] != "" {
		missing++
	}

	if err := RegisterTable2(centre, version, map[int]string{code: "overridden"}); err != nil {
		t.Fatalf("RegisterTable2: %v", err)
	}
	if got := parameterName(centre, 0, version, code); got != "overridden" {
		t.Errorf("注册后 = %q，想要 overridden", got)
	}
	if got, want := parameterName(centre, 0, version, missing), strconv.Itoa(missing); got != want {
		t.Errorf("注册表里缺的码 = %q，想要 %q（不回落到内置表）", got, want)
	}

	// Restore the built-in rows (no unregister exists) for the rest of the package.
	restore := make(map[int]string, len(builtin))
	for c, name := range builtin {
		restore[int(c)] = name
	}
	if err := RegisterTable2(centre, version, restore); err != nil {
		t.Fatalf("还原 RegisterTable2: %v", err)
	}
	if got := parameterName(centre, 0, version, code); got != builtinName {
		t.Errorf("还原后 = %q，想要 %q", got, builtinName)
	}
	if versions := Table2Versions(); len(versions) == 0 {
		t.Error("Table2Versions 应当列出调用方注册过的 (centre, version)")
	}
}

// TestBuiltinTable2Shape 是生成数据的结构不变量：45 张可达表、每张非空、码在
// 0..255、题名非空；两张不可达的表（2.82.1.table、2.233.1.table）不在里面。
func TestBuiltinTable2Shape(t *testing.T) {
	if got := len(genTable2); got != 45 {
		t.Errorf("内置表数 = %d，想要 45（paramdata/grib1 里 47 张 2.<centre>.<version>.table 减去两张不可达的）", got)
	}
	names := 0
	for key, tbl := range genTable2 {
		if len(tbl) == 0 {
			t.Errorf("内置表 %v 是空的", key)
		}
		for code, name := range tbl {
			names++
			if name == "" {
				t.Errorf("内置表 %v 码 %d 的题名为空", key, code)
			}
		}
	}
	if names != 4985 {
		t.Errorf("内置题名总数 = %d，想要 4985", names)
	}
	if tbl, ok := genTable2[[2]uint8{82, 1}]; ok {
		t.Errorf("2.82.1.table 不该在内置表里（table2Version < 128 走 WMO），却有 %d 条", len(tbl))
	}
	if tbl, ok := genTable2[[2]uint8{233, 1}]; ok {
		t.Errorf("2.233.1.table 不该在内置表里（table2Version < 128 走 WMO），却有 %d 条", len(tbl))
	}
	// WMO 国际交换表必须在（version 1..3，centre 段是 0）。
	for v := 1; v <= 3; v++ {
		if len(genTable2[[2]uint8{0, uint8(v)}]) == 0 {
			t.Errorf("WMO 表 2.0.%d.table 不在内置表里", v)
		}
	}
}
