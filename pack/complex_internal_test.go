// 模板 5.2/5.3 复杂解包器（complexDecode）的失败即报错守卫单元测试。
// 这些守卫是 bug.md §1.1 那批修正的核心：NG==0 常值场、未标定的空间差分阶
// 与缺测管理、非法 origType —— 一律拒绝猜测。之前只在集成夹具里间接覆盖，
// 这里直接喂最小头部逐个锁死。

package pack

import (
	"bytes"
	"testing"
)

// TestComplexDecodeFailLoud 锁死 complexDecode 在头部校验阶段的所有失败即报错路径。
// 这些分支在真正读位流之前就返回，所以 data 传 nil 即可。
func TestComplexDecodeGuards(t *testing.T) {
	const ng = 1
	base := ComplexPacking{
		R: 0, B: 0, D: 0, BitsPerVal: 0, OrigType: 0,
		GroupSplit: 0, MissingMgmt: 0,
		PrimaryMiss: 0, SecondaryMiss: 0,
		NumGroups: ng, RefGroupWidths: 0, BitsGroupWidths: 0,
		RefGroupLengths: 0, LenIncrement: 0, TrueLastLen: 0,
		BitsScaledLens: 0, OrderSpatialDiff: 0, OctetsExtraDesc: 0,
	}
	cases := []struct {
		name string
		mut  func(*ComplexPacking)
		want string
	}{
		{"NG==0", func(p *ComplexPacking) { p.NumGroups = 0 }, "numberOfGroupsOfDataValues = 0"},
		{"spatial diff order 3", func(p *ComplexPacking) { p.OrderSpatialDiff = 3 }, "order of spatial differencing"},
		{"order>0 but no extra descriptors", func(p *ComplexPacking) { p.OrderSpatialDiff = 1; p.OctetsExtraDesc = 0 }, "number of octets extra descriptors"},
		{"missing value management", func(p *ComplexPacking) { p.MissingMgmt = 1 }, "missing value management used"},
		{"origType 2", func(p *ComplexPacking) { p.OrigType = 2 }, "type of original field values"},
	}
	for _, c := range cases {
		p := base
		c.mut(&p)
		_, err := complexDecode(TemplateComplex, p, nil, 0)
		if err == nil {
			t.Errorf("%s: want error, got nil", c.name)
			continue
		}
		if !bytes.Contains([]byte(err.Error()), []byte(c.want)) {
			t.Errorf("%s: error %q does not mention %q", c.name, err.Error(), c.want)
		}
	}
	// 合法配置（模板 5.2 纯复杂、无空间差分、无缺测）应当通过全部头部守卫；
	// 它绝不能落入上面那些「失败即报错」分支（具体的位流读取由集成夹具覆盖）。
	// 这里零宽分组 + numValues=0 下没有可读位，函数返回 (空, nil) 是合法的，
	// 关键只在于它没被某个守卫误拒。
	p := base
	_, err := complexDecode(TemplateComplexPlain, p, nil, 0)
	if err != nil {
		guardMsgs := []string{
			"numberOfGroupsOfDataValues",
			"order of spatial differencing",
			"number of octets extra descriptors",
			"missing value management used",
			"type of original field values",
		}
		for _, m := range guardMsgs {
			if bytes.Contains([]byte(err.Error()), []byte(m)) {
				t.Errorf("valid 5.2 header wrongly rejected by guard %q: %v", m, err)
			}
		}
	}
}
