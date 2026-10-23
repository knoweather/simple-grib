package paramdb

import "testing"

// localTablesVersion 是 ecCodes 概念条件用的键名（grib2/section.1.def 的
// `codetable[1] localTablesVersion`）。本库曾只提供 localTablesVersionNumber，
// 使 ecmf 的 1200 处本地条目永不命中、一律回退 master。这个测试锁住修复：
// 带 localTablesVersion=1 的查询必须命中 ecmf 本地表（sro 等），
// 而 localTablesVersion=0 时不应命中那条（条件要求 =1）。
//
// Expectations come from ecCodes 2.47.0:
//
//	sro  -> shortName 'sro',  name 'Surface runoff',                  paramId 8
//	ssro -> shortName 'ssro', name 'Sub-surface runoff',              paramId 9
func TestLocalTablesVersionMatchesEcmfLocalTable(t *testing.T) {
	base := Map{
		"centre":                      "98",
		"discipline":                  "2",
		"parameterCategory":           "0",
		"parameterNumber":             "202",
		"typeOfFirstFixedSurface":     "1",
		"typeOfStatisticalProcessing": "1",
		"mtg2":                        "1",
	}

	t.Run("localTablesVersion=1 hits ecmf local table", func(t *testing.T) {
		q := Map{}
		for k, v := range base {
			q[k] = v
		}
		q["localTablesVersion"] = "1"

		sn, ok := ShortName(q)
		if !ok || sn != "sro" {
			t.Fatalf("ShortName = %q,%v; want \"sro\",true", sn, ok)
		}
		if name, ok := Name(q); !ok || name != "Surface runoff" {
			t.Errorf("Name = %q,%v; want \"Surface runoff\",true", name, ok)
		}
		if pid, ok := ParamID(q); !ok || pid != 8 {
			t.Errorf("ParamID = %d,%v; want 8,true", pid, ok)
		}
	})

	t.Run("localTablesVersion=0 does not match that entry", func(t *testing.T) {
		q := Map{}
		for k, v := range base {
			q[k] = v
		}
		q["localTablesVersion"] = "0"

		if sn, ok := ShortName(q); ok && sn == "sro" {
			t.Errorf("ShortName = %q; must not be %q when localTablesVersion=0", sn, "sro")
		}
	})

	t.Run("localTablesVersionNumber alone does not satisfy concept conditions", func(t *testing.T) {
		// 概念条件写的是 localTablesVersion，故只给旧键名的 Map 不应命中 sro。
		// QueryFrom 两个键都给，所以真实消息不受影响；这里锁住"键名不能写错"。
		q := Map{}
		for k, v := range base {
			q[k] = v
		}
		q["localTablesVersionNumber"] = "1"
		if sn, ok := ShortName(q); ok && sn == "sro" {
			t.Errorf("ShortName = %q; concept conditions use localTablesVersion, not localTablesVersionNumber", sn)
		}
	})
}
