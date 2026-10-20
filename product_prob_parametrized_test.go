package grib

import (
	"path/filepath"
	"testing"
)

// 夹具由 ecCodes 2.47.0 生成（同样的脚本可重建，见该目录 README.md）：
// 4.112 与 4.123 各一件 NT=1、一件 NT=3 的消息，概率块的 type/上下限与统计
// 处理字节都写入了可辨认的哨兵值。下面的期望值全部来自 ecCodes 自己的
// codes_get，不是本库的解析结果，所以这组用例是真正的双端对照。
//
// Fixtures were produced by ecCodes 2.47.0 (see README.md in the directory):
// one NT=1 and one NT=3 message for each of templates 4.112 and 4.123, with
// sentinel values in the probability block and the statistical-process byte.
// Every expectation below comes from ecCodes' own codes_get, not from this
// library, so the case is a genuine two-sided comparison.
func TestProbabilityParametrizedEcCodesFixtures(t *testing.T) {
	cases := []struct {
		file string
		tmpl int
		nt   int
	}{
		{"p112_nt1.grib2", 112, 1},
		{"p112_nt3.grib2", 112, 3},
		{"p123_nt1.grib2", 123, 1},
		{"p123_nt3.grib2", 123, 3},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			f, err := Open(filepath.Join("testdata", "prob-parametrized", c.file))
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer f.Close()
			m, err := f.Message(0)
			if err != nil {
				t.Fatalf("Message(0): %v", err)
			}
			p, err := m.Product()
			if err != nil {
				t.Fatalf("Product: %v", err)
			}
			if p.TemplateNumber != c.tmpl {
				t.Fatalf("TemplateNumber = %d, want %d", p.TemplateNumber, c.tmpl)
			}
			// Probability type 5 = "probability of event equal to or above
			// the lower limit"? 5 是夹具写入的哨兵；ecCodes 读回同值。
			if p.ProbabilityType != 5 {
				t.Errorf("ProbabilityType = %d, want 5", p.ProbabilityType)
			}
			if got := p.ProbabilityLower; got == nil || !got.HasValue ||
				got.ScaleFactorRaw != 2 || got.ScaledValueRaw != 4444 {
				t.Errorf("ProbabilityLower = %+v, want raw (2,4444) with value", got)
			}
			if got := p.ProbabilityUpper; got == nil || !got.HasValue ||
				got.ScaleFactorRaw != 3 || got.ScaledValueRaw != 5555 {
				t.Errorf("ProbabilityUpper = %+v, want raw (3,5555) with value", got)
			}
			// ecCodes 的 typeOfStatisticalProcessing 对 NT=3 也报 1：它映射到
			// 重复块的**最后**一条记录，而夹具正是通过标量键写入的。
			if p.TypeOfStatisticalProcessing != 1 {
				t.Errorf("TypeOfStatisticalProcessing = %d, want 1 (ecCodes reads the last time-range record)",
					p.TypeOfStatisticalProcessing)
			}
		})
	}
}