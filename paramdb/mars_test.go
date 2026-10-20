package paramdb

import "testing"

// TestMarsClassSelectsPseudoCentreDataset 验证 marsClass 能让按数据集圈定的
// 伪中心目录胜出，且普通 ECMWF 报文不被它们影响。
//
// 判别用例取 era 与 ecmf **结果不同**的那个条目：lnsp 条件
// （discipline 0 / parameterCategory 3 / parameterNumber 25 /
// typeOfFirstFixedSurface 105 / typeOfStatisticalProcessing 0）
//
//	era 目录 -> shortName 'lnsp'    （paramId 152）
//	ecmf 目录 -> shortName 'avg_lnsp'（paramId 235152）
//
// 挂载时 era 目录的条目额外带 marsClass=22 条件，故 ERA 报文命中它（条件更多
// 者胜），而运行态报文只命中 ecmf、无 MARS 标签的报文同样只命中 ecmf。
func TestMarsClassSelectsPseudoCentreDataset(t *testing.T) {
	base := Map{
		"centre":                      "98",
		"mtg2":                        "0",
		"discipline":                  "0",
		"parameterCategory":           "3",
		"parameterNumber":             "25",
		"typeOfFirstFixedSurface":     "105",
		"typeOfStatisticalProcessing": "0",
	}
	clone := func(extra Map) Map {
		q := Map{}
		for k, v := range base {
			q[k] = v
		}
		for k, v := range extra {
			q[k] = v
		}
		return q
	}

	t.Run("era dataset (marsClass=22) resolves via era dir", func(t *testing.T) {
		q := clone(Map{"marsClass": "22"})
		sn, ok := ShortName(q)
		if !ok || sn != "lnsp" {
			t.Errorf("ShortName = %q,%v; want \"lnsp\",true", sn, ok)
		}
		if pid, ok := ParamID(q); !ok || pid != 152 {
			t.Errorf("ParamID = %d,%v; want 152,true", pid, ok)
		}
	})

	t.Run("operational (marsClass=1) is unaffected by era dir", func(t *testing.T) {
		q := clone(Map{"marsClass": "1"})
		sn, ok := ShortName(q)
		if !ok || sn != "avg_lnsp" {
			t.Errorf("ShortName = %q,%v; want \"avg_lnsp\",true", sn, ok)
		}
	})

	t.Run("no MARS labelling falls back to ecmf", func(t *testing.T) {
		q := clone(nil)
		sn, ok := ShortName(q)
		if !ok || sn != "avg_lnsp" {
			t.Errorf("ShortName = %q,%v; want \"avg_lnsp\",true", sn, ok)
		}
	})

	// cerise 只有 .lte33 变体，条目带 mtg2=0 条件。sro 在 cerise 与 ecmf 同结果，
	// 该用例证明 marsClass=50 时链路仍解析出 token（不落空）。
	t.Run("cerise dataset (marsClass=50) still resolves sro", func(t *testing.T) {
		q := Map{
			"centre":                      "98",
			"marsClass":                   "50",
			"mtg2":                        "0",
			"localTablesVersion":          "1",
			"discipline":                  "2",
			"parameterCategory":           "0",
			"parameterNumber":             "202",
			"typeOfFirstFixedSurface":     "1",
			"typeOfStatisticalProcessing": "1",
		}
		if sn, ok := ShortName(q); !ok || sn != "sro" {
			t.Errorf("ShortName = %q,%v; want \"sro\",true", sn, ok)
		}
	})
}
