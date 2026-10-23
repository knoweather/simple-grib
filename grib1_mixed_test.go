package grib

import (
	"math"
	"os"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// ibmEncode32 encodes a float64 as GRIB1 IBM single precision (4 bytes, big
// endian). Test helper: only tested against the normal value ranges used here.
func ibmEncode32(v float64) [4]byte {
	if v == 0 {
		return [4]byte{}
	}
	sign := byte(0)
	if v < 0 {
		sign = 0x80
		v = -v
	}
	// v = m/2^24 * 16^(e-64), m ∈ [2^20, 2^24)
	e := 64
	for v >= 1.0 {
		v /= 16.0
		e++
	}
	for v < 1.0/16.0 {
		v *= 16.0
		e--
	}
	m := uint32(math.Round(v * (1 << 24)))
	if m >= 1<<24 { // rounding carried over
		m >>= 1
		e++
	}
	return [4]byte{sign | byte(e), byte(m >> 16), byte(m >> 8), byte(m)}
}

// buildGRIB1ConstMsg builds a minimal GRIB1 message: a 2×2 regular
// lat/lon grid with a constant field (BDS bit width 0), all values equal to
// vConst. Layout follows the ecCodes grib1 definitions.
func buildGRIB1ConstMsg(t *testing.T, vConst float64) []byte {
	t.Helper()
	pds := make([]byte, 28)
	pds[1], pds[2] = 0, 28                                         // section length 28 (3-byte big endian, octets 1-3)
	pds[3] = 3                                                     // table 2 version
	pds[4] = 98                                                    // centre: ECMWF
	pds[6] = 255                                                   // grid id: 255 = defined by the GDS
	pds[7] = 0x80                                                  // flags: GDS present, no BMS
	pds[8] = 11                                                    // parameter 11 (2m temperature)
	pds[9] = 105                                                   // level type 105 (height above ground)
	pds[10], pds[11] = 0x07, 0xD0                                  // level value 2000
	pds[12], pds[13], pds[14], pds[15], pds[16] = 25, 12, 29, 0, 0 // reference time
	pds[17] = 1                                                    // time unit: hours
	pds[24] = 21                                                   // century
	// pds[26:28]: decimal scale factor D = 0

	gds := make([]byte, 32)
	gds[1], gds[2] = 0, 32
	gds[3] = 0                    // NV = 0
	gds[4] = 255                  // PV = 255
	gds[5] = 0                    // 类型 0：规则经纬
	gds[6], gds[7] = 0, 2         // Ni = 2
	gds[8], gds[9] = 0, 2         // Nj = 2
	putSmag24(gds[10:13], 60_000) // La1 = 60N
	putSmag24(gds[13:16], 0)      // Lo1 = 0E
	gds[16] = 0x80                // 分辨率标志（位 1 = 1）
	putSmag24(gds[17:20], 30_000) // La2 = 30N
	putSmag24(gds[20:23], 10_000) // Lo2 = 10E
	gds[23], gds[24] = 0x03, 0xE8 // Di = 1000（=10°）
	gds[25], gds[26] = 0x03, 0xE8 // Dj = 1000（=10°）
	gds[27] = 0                   // 扫描模式 WE:SN

	bds := make([]byte, 12)
	bds[1], bds[2] = 0, 12
	bds[3] = 0            // 标志：无附加标志
	bds[4], bds[5] = 0, 0 // E = 0
	r := ibmEncode32(vConst)
	copy(bds[6:10], r[:])
	bds[10] = 0 // 位宽 0 => 常量场

	total := 8 + len(pds) + len(gds) + len(bds) + 4
	msg := make([]byte, 0, total)
	msg = append(msg, 'G', 'R', 'I', 'B',
		byte(total>>16), byte(total>>8), byte(total), 1)
	msg = append(msg, pds...)
	msg = append(msg, gds...)
	msg = append(msg, bds...)
	msg = append(msg, '7', '7', '7', '7')
	return msg
}

// putSmag24 写 GRIB1 的 signed[3] 符号-幅度整数（1/1000 度单位由调用方决定）。
func putSmag24(b []byte, v int) {
	sign := byte(0)
	if v < 0 {
		sign = 0x80
		v = -v
	}
	b[0] = sign | byte(v>>16)
	b[1] = byte(v >> 8)
	b[2] = byte(v)
}

// TestMixedEditionFile 验证 GRIB1 与 GRIB2 混装文件：每条消息按自己的版本解析，
// Message.Edition() 区分；这覆盖 openReader 的版本分派与统一门面。
func TestMixedEditionFile(t *testing.T) {
	g2, err := os.ReadFile("testdata/ecc-xcheck/poscorners.grib2")
	if err != nil {
		t.Skip("缺 GRIB2 样本:", err)
	}
	g1 := buildGRIB1ConstMsg(t, 273.15)

	mixed := make([]byte, 0, len(g1)+len(g2))
	mixed = append(mixed, g1...)
	mixed = append(mixed, g2...)

	fl, err := OpenReader(newReaderAt(mixed), int64(len(mixed)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	n, _ := fl.NumMessages()
	if n != 2 {
		t.Fatalf("消息数 = %d, 想要 2", n)
	}

	m0, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	if m0.Edition() != 1 {
		t.Errorf("msg0 edition = %d, 想要 1", m0.Edition())
	}
	p0, err := m0.Product()
	if err != nil {
		t.Fatalf("msg0 Product: %v", err)
	}
	if p0.Edition != 1 || p0.ParameterNumber != 11 || p0.TemplateNumber != -1 {
		t.Errorf("msg0 product = %+v", p0)
	}
	gd0, err := m0.Grid()
	if err != nil {
		t.Fatalf("msg0 Grid: %v", err)
	}
	if gd0.Ni() != 2 || gd0.Nj() != 2 {
		t.Errorf("msg0 网格 = %dx%d", gd0.Ni(), gd0.Nj())
	}
	v0, err := m0.Values64()
	if err != nil {
		t.Fatalf("msg0 Values64: %v", err)
	}
	if len(v0) != 4 {
		t.Fatalf("msg0 值数 = %d, 想要 4", len(v0))
	}
	dr0, err := m0.DataRep()
	if err != nil {
		t.Fatalf("msg0 DataRep: %v", err)
	}
	if dr0.NumValues != 4 {
		t.Errorf("常量场 DataRep.NumValues = %d, 想要 4", dr0.NumValues)
	}
	for i, v := range v0 {
		if math.Abs(v-273.15) > 1e-3 {
			t.Errorf("msg0 值[%d] = %v, 想要 273.15（IBM 浮点常量场）", i, v)
		}
	}

	m1, err := fl.Message(1)
	if err != nil {
		t.Fatalf("Message(1): %v", err)
	}
	if m1.Edition() != 2 {
		t.Errorf("msg1 edition = %d, 想要 2", m1.Edition())
	}
	p1, err := m1.Product()
	if err != nil {
		t.Fatalf("msg1 Product: %v", err)
	}
	// msg1 的 fixture 是 188 字节的 ECMWF eccodes 样本，第 1 节声明
	// masterTablesVersionNumber = 0 —— 没有 tables/0 目录。
	//
	// ecCodes 的 parameterName 于是退化成十进制码（CodetableTitle::
	// unpack_string 在拿不到表条目时 snprintf("%d", value)）：
	//
	//   $ python -c "import eccodes; h=eccodes.codes_grib_new_from_file(open('testdata/ecc-xcheck/poscorners.grib2','rb')); print(eccodes.codes_get(h,'tablesVersion'), repr(eccodes.codes_get(h,'parameterName')))"
	//   0 '0'
	//
	// 因此版本化的 Product.ParameterName（spec.ParameterNameFor）在这里是 "0"；
	// 版本无关、恒用最新 WMO 表的 spec.ParameterName 仍然是 "Temperature"。
	// 两者是_不同_的语义，各自在此钉住。
	if p1.Edition != 2 || p1.ParameterName != "0" {
		t.Errorf("msg1 product = %+v", p1)
	}
	if name, err := spec.ParameterName(0, 0, 0); err != nil || name != "Temperature" {
		t.Errorf("spec.ParameterName(0,0,0) = %q, %v; 想要 Temperature（最新表视图，与版本化 API 语义不同）", name, err)
	}
	if name := spec.ParameterNameFor(spec.ParamNameRef{MasterVersion: 0, LocalVersion: 0, Centre: 98}); name != "0" {
		t.Errorf("spec.ParameterNameFor(v0) = %q; 想要 \"0\"（ecCodes 在无表版本时退化到十进制码）", name)
	}
}

// newReaderAt 包一层 []byte 成 io.ReaderAt（OpenReader 需要）。
func newReaderAt(b []byte) *byteReaderAt { return &byteReaderAt{b: b} }

type byteReaderAt struct{ b []byte }

func (r *byteReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(r.b)) {
		return 0, os.ErrClosed // EOF 语义即可，测试不区分
	}
	n := copy(p, r.b[off:])
	if n < len(p) {
		return n, os.ErrClosed
	}
	return n, nil
}

// TestMultiGRIB1MessageFile 多条 GRIB1 消息 + Messages() 迭代器 + float32 视图：
// 根包对纯 GRIB1 文件的全链集成。
func TestMultiGRIB1MessageFile(t *testing.T) {
	g1a := buildGRIB1ConstMsg(t, 273.15)
	g1b := buildGRIB1ConstMsg(t, 280.5)

	multi := make([]byte, 0, len(g1a)+len(g1b))
	multi = append(multi, g1a...)
	multi = append(multi, g1b...)

	fl, err := OpenReader(newReaderAt(multi), int64(len(multi)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	n, _ := fl.NumMessages()
	if n != 2 {
		t.Fatalf("消息数 = %d, 想要 2", n)
	}

	// Messages() 迭代器：两条都走到，edition 全对
	count := 0
	for m, err := range fl.Messages() {
		if err != nil {
			t.Fatalf("迭代消息 %d: %v", count, err)
		}
		if m.Edition() != 1 {
			t.Errorf("消息 %d edition = %d", count, m.Edition())
		}
		count++
	}
	if count != 2 {
		t.Errorf("迭代器走过 %d 条, 想要 2", count)
	}

	// float32 视图（Values()）：第二条消息的值应当对得上
	m1, err := fl.Message(1)
	if err != nil {
		t.Fatalf("Message(1): %v", err)
	}
	v32, err := m1.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(v32) != 4 || math.Abs(float64(v32[0])-280.5) > 1e-2 {
		t.Errorf("float32 值 = %v, 想要 4 个 ≈280.5", v32)
	}
}
