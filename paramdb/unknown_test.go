package paramdb

import "testing"

// 本文件钉住 ecCodes 对「概念链完全没命中」的渲染：shortName / name / units /
// typeOfLevel = "unknown"，paramId = 0。ecCodes 2.47 与 2.50 相同：
// parameterCategory/parameterNumber 取表里没有的值时前四个键是 "unknown"，
// typeOfFirstFixedSurface 取 200 或 255 时 typeOfLevel 也是 "unknown"。
//
// 同时钉住 (值, ok) 形状没被破坏：那是「消息里到底有没有这个参数」的唯一依据。
func TestUnknownRendering(t *testing.T) {
	miss := Map{
		"discipline":              "0",
		"parameterCategory":       "192",
		"parameterNumber":         "255",
		"typeOfFirstFixedSurface": "200",
	}
	if v, ok := ShortName(miss); ok {
		t.Fatalf("这组键竟然命中了 shortName=%q；换一组「肯定没命中」的键", v)
	}

	if _, ok := ShortName(miss); ok {
		t.Error("ShortName 的 ok 必须是 false（没命中）")
	}
	if got := ShortNameOrUnknown(miss); got != Unknown {
		t.Errorf("ShortNameOrUnknown = %q，想要 %q", got, Unknown)
	}
	if got := NameOrUnknown(miss); got != Unknown {
		t.Errorf("NameOrUnknown = %q，想要 %q", got, Unknown)
	}
	if got := UnitsOrUnknown(miss); got != Unknown {
		t.Errorf("UnitsOrUnknown = %q，想要 %q", got, Unknown)
	}
	if got := TypeOfLevelOrUnknown(miss); got != Unknown {
		t.Errorf("TypeOfLevelOrUnknown = %q，想要 %q", got, Unknown)
	}
	if got := ParamIDOrZero(miss); got != 0 {
		t.Errorf("ParamIDOrZero = %d，想要 0（ecCodes 的 paramId 兜底）", got)
	}
	if Unknown != "unknown" {
		t.Errorf("Unknown = %q，ecCodes 的字面量是 \"unknown\"", Unknown)
	}

	// 命中时 *OrUnknown / *OrZero 必须与 (值, ok) 视图逐字一致。用例取
	// paramdb_test.go 里已有的权威锚点：0/0/0 + 离地 2 米 → 2t。
	hit := q2(
		"typeOfFirstFixedSurface", "103",
		"scaleFactorOfFirstFixedSurface", "0",
		"scaledValueOfFirstFixedSurface", "2",
	)
	want, ok := ShortName(hit)
	if !ok || want != "2t" {
		t.Fatalf("(0,0,0)+2m 应当命中 2t，得到 %q,%v", want, ok)
	}
	if got := ShortNameOrUnknown(hit); got != want {
		t.Errorf("ShortNameOrUnknown(命中) = %q，想要 %q", got, want)
	}
	if want, ok := Name(hit); ok {
		if got := NameOrUnknown(hit); got != want {
			t.Errorf("NameOrUnknown(命中) = %q，想要 %q", got, want)
		}
	} else {
		t.Error("2t 的 Name 应当命中")
	}
	if want, ok := Units(hit); ok {
		if got := UnitsOrUnknown(hit); got != want {
			t.Errorf("UnitsOrUnknown(命中) = %q，想要 %q", got, want)
		}
	} else {
		t.Error("2t 的 Units 应当命中")
	}
	if want, ok := TypeOfLevel(hit); ok {
		if got := TypeOfLevelOrUnknown(hit); got != want {
			t.Errorf("TypeOfLevelOrUnknown(命中) = %q，想要 %q", got, want)
		}
	} else {
		t.Error("2t 的 TypeOfLevel 应当命中")
	}
	if want, ok := ParamID(hit); ok {
		if got := ParamIDOrZero(hit); got != want {
			t.Errorf("ParamIDOrZero(命中) = %d，想要 %d", got, want)
		}
	} else {
		t.Error("2t 的 ParamID 应当命中")
	}
}
