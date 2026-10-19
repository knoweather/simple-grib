// 模板 5.0 简单解包器（pack.simpleDecode / pack.scaleSimple）单元测试。
// 之前这些零件只在 Unpack 集成测试里间接覆盖；这里直接喂合成位流，
// 锁死 bits>32 无条件拒绝、常值场、origType 校验、payload 长度，以及
// 公式的精确运算顺序（eZero/dZero 四个分支）。

package pack

import (
	"math"
	"testing"
)

// TestSimpleDecodeTrivial 走 16 位快速路径：value = raw（R=E=D=0）。
// 9 个 16 位值需要 18 字节（大端存各原始值）。
func TestSimpleDecodeTrivial(t *testing.T) {
	data := make([]byte, 18)
	for i := 0; i < 9; i++ {
		data[2*i] = byte(i >> 8)
		data[2*i+1] = byte(i)
	}
	got, err := simpleDecode(SimplePacking{Bits: 16, OrigType: 0}, data, 9)
	if err != nil {
		t.Fatalf("simpleDecode: %v", err)
	}
	for i := range got {
		if got[i] != float32(i) {
			t.Errorf("value[%d] = %v, want %d", i, got[i], i)
		}
	}
}

// TestSimpleDecodeConstantField 走 bits=0 分支：每个值都是 (0×2^E + R)×10^(−D)。
// 数据段必须为 0 字节。
func TestSimpleDecodeConstantField(t *testing.T) {
	got, err := simpleDecode(SimplePacking{R: 273.15, E: 0, D: 2, Bits: 0, OrigType: 0}, nil, 4)
	if err != nil {
		t.Fatalf("simpleDecode: %v", err)
	}
	const want = float32(2.7315)
	for i := range got {
		if got[i] != want {
			t.Errorf("value[%d] = %v, want %v (R×10^(−D))", i, got[i], want)
		}
	}
	// 但给非零数据段就应当失败（常值场没有位流）。
	if _, err := simpleDecode(SimplePacking{Bits: 0, OrigType: 0}, []byte{0}, 4); err == nil {
		t.Error("bits=0 with non-empty data: want error, got nil")
	}
}

// TestSimpleDecodeBitsTooWide 锁死 bug.md §2.5 的「绝不静默」契约：
// bits>32 无条件拒绝，即便 numberOfValues=0 也要拒（旧代码的
// `&& numValues > 0` 守卫会让这种非法声明悄悄通过）。
func TestSimpleDecodeBitsTooWide(t *testing.T) {
	// numValues=0：want=(33*0+7)/8=0，长度检查会被糊弄过去，必须靠 bits>32 守卫抓到。
	if _, err := simpleDecode(SimplePacking{Bits: 33, OrigType: 0}, nil, 0); err == nil {
		t.Error("bits=33 numValues=0: want error, got nil")
	}
	// numValues=9 且数据段长度也对（38 字节）：长度检查通过，仍要被 bits>32 抓到。
	if _, err := simpleDecode(SimplePacking{Bits: 33, OrigType: 0}, make([]byte, 38), 9); err == nil {
		t.Error("bits=33 numValues=9: want error, got nil")
	}
	// 边界：bits=32 必须合法。
	if _, err := simpleDecode(SimplePacking{Bits: 32, OrigType: 0}, make([]byte, 36), 9); err != nil {
		t.Errorf("bits=32 should be valid: %v", err)
	}
}

// TestSimpleDecodePayloadLength 数据段长度必须恰好等于 ceil(numValues×bits/8)。
func TestSimpleDecodePayloadLength(t *testing.T) {
	if _, err := simpleDecode(SimplePacking{Bits: 8, OrigType: 0}, make([]byte, 5), 9); err == nil {
		t.Error("9×8 bits needs 9 bytes, got 5: want error, got nil")
	}
	if _, err := simpleDecode(SimplePacking{Bits: 8, OrigType: 0}, make([]byte, 9), 9); err != nil {
		t.Errorf("correct 9-byte payload should pass: %v", err)
	}
}

// TestSimpleDecodeOrigType 表 5.1 只定义了 0（浮点）和 1（整数）；其他值
// 必须以 ErrUnsupportedValue 拒绝，绝不替你猜默认值。
func TestSimpleDecodeOrigType(t *testing.T) {
	_, err := simpleDecode(SimplePacking{Bits: 8, OrigType: 2}, make([]byte, 9), 9)
	if err == nil {
		t.Fatal("origType=2: want error, got nil")
	}
	eu, ok := err.(*ErrUnsupportedValue)
	if !ok {
		t.Fatalf("error type = %T, want *ErrUnsupportedValue", err)
	}
	if eu.Field != "type of original field values" || eu.Value != 2 {
		t.Errorf("ErrUnsupportedValue = %+v", eu)
	}
}

// TestSimpleDecodeScaling 锁死符号-幅度 E/D 与十进制 D 的缩放，以及
// 非 8 整数倍的位宽（走 unpackWide 滑动窗口路径）。
func TestSimpleDecodeScaling(t *testing.T) {
	// D=1：value = raw × 10^(−1)。
	got, err := simpleDecode(SimplePacking{D: 1, Bits: 8, OrigType: 0}, []byte{6, 7, 8}, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range []float32{0.6, 0.7, 0.8} {
		if got[i] != w {
			t.Errorf("[D=1] value[%d] = %v, want %v", i, got[i], w)
		}
	}
	// E=-3（符号-幅度）：value = raw × 2^(−3)。
	got, err = simpleDecode(SimplePacking{E: -3, Bits: 8, OrigType: 0}, []byte{64, 128, 192}, 3)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range []float32{8, 16, 24} {
		if got[i] != w {
			t.Errorf("[E=-3] value[%d] = %v, want %v", i, got[i], w)
		}
	}
	// bits=12：走 unpackWide；R=E=D=0 时 value = raw。
	got, err = simpleDecode(SimplePacking{Bits: 12, OrigType: 0}, []byte{0x12, 0x34, 0x56}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != 0x123 || got[1] != 0x456 {
		t.Errorf("[bits=12] got [%v %v], want [0x123 0x456]", got[0], got[1])
	}
}

// TestScaleSimple 直接锁死模板 5.0 公式的四个运算顺序分支：
// (raw × 2^E + R) × 10^(−D)，用 eZero/dZero 标志跳过乘精确 1.0。
// 任何一个分支的运算顺序写错，float 结果都会偏离。
func TestScaleSimple(t *testing.T) {
	cases := []struct {
		name                string
		raw                 uint32
		twoE, fR, tenMinusD float64
		eZero, dZero        bool
		want                float32
	}{
		{"E=0 D=0", 5, 1.0, 0.0, 1.0, true, true, 5},
		{"E set D=0", 5, 2.0, 1.0, 1.0, false, true, 11},     // 5*2+1
		{"E=0 D set", 5, 1.0, 1.0, 0.1, true, false, 0.6},    // (5+1)*0.1
		{"E set D set", 5, 2.0, 1.0, 0.1, false, false, 1.1}, // (5*2+1)*0.1
	}
	for _, c := range cases {
		got := scaleSimple(c.raw, c.twoE, c.fR, c.tenMinusD, c.eZero, c.dZero)
		if got != c.want {
			t.Errorf("%s: scaleSimple = %v, want %v", c.name, got, c.want)
		}
	}
	// 一个非整数结果的精确性检查，确保公式全程 float64 再四舍五入成 float32。
	got := scaleSimple(1, math.Pow(2, -2), 0.1, 0.1, false, false) // (1*0.25+0.1)*0.1 = 0.035
	if got != float32(0.035) {
		t.Errorf("scaleSimple(0.035 path) = %v, want 0.035", got)
	}
}
