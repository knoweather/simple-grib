package spec

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Value 是一个模板字段解析后的取值。普通字段的 U 存无符号整数读值
// （F32 字段还会填 F）；Variable 与 Symbolic 字段的值为 0，调用方要改用
// Walk.Tail / Expr 去取。Octet 是字段起始处基于 1 的 section octet 号。
type Value struct {
	Octet int
	Kind  Kind
	U     uint64
	F     float32
}

// Walk 是一个模板的解析实例：定义里的每个字段都映射到实际 section 的字节上。
// 同一个 *Walk 可以在各包之间只读共享（核心逻辑会把它传入 grid/pack 的上下文）。
type Walk struct {
	def  *TemplateDef
	vals []Value // 与 def.Fields 平行；非普通字段为全零 Value
	data []byte  // 完整 section 字节，从 octet 1 起（含 4 字节长度前缀）
}

// NewWalk 在 sectionData 上解析 def。sectionData 必须是完整的 section 字节，
// 从 octet 1 起（含 4 字节长度前缀）；若某字段的固定范围超出数据则报错。
// Symbolic 与 Variable 字段在此不读取。
func NewWalk(def *TemplateDef, sectionData []byte) (*Walk, error) {
	if def == nil {
		return nil, fmt.Errorf("spec: NewWalk with nil definition")
	}
	w := &Walk{def: def, data: sectionData, vals: make([]Value, len(def.Fields))}
	for i, f := range def.Fields {
		switch {
		case f.Kind == KindVariable:
			// 尾部通过 Walk.Tail 访问，没有固定字节可读。
			w.vals[i] = Value{Octet: f.Start + 1, Kind: KindVariable}
		case f.Kind == KindSymbolic || f.Start < 0:
			w.vals[i] = Value{Kind: KindSymbolic}
		default:
			if f.Count <= 0 {
				return nil, fmt.Errorf("spec: field %d of template %s has no width", i, def.ID)
			}
			end := f.Start + f.Count
			if end > len(sectionData) {
				return nil, fmt.Errorf("spec: template %s field at octet %d needs %d bytes, section has only %d",
					def.ID, f.Start+1, end, len(sectionData))
			}
			b := sectionData[f.Start : f.Start+f.Count]
			v := Value{Octet: f.Start + 1, Kind: f.Kind}
			switch f.Kind {
			case KindU8:
				v.U = uint64(b[0])
			case KindU16:
				v.U = uint64(binary.BigEndian.Uint16(b))
			case KindU32:
				v.U = uint64(binary.BigEndian.Uint32(b))
			case KindF32:
				v.F = math.Float32frombits(binary.BigEndian.Uint32(b))
				v.U = uint64(math.Float32bits(v.F))
			default:
				return nil, fmt.Errorf("spec: template %s field %d has unhandled kind %d", def.ID, i, f.Kind)
			}
			w.vals[i] = v
		}
	}
	return w, nil
}

// Def 返回该 walk 所基于的模板定义。
func (w *Walk) Def() *TemplateDef { return w.def }

// Len 是字段数量（= len(Def().Fields)）。
func (w *Walk) Len() int { return len(w.vals) }

// At 返回第 i 个字段（索引对应 Def().Fields）的取值。索引落在 [0, Len())
// 之外属于编程错误，会返回零值 Value{Octet:0,Kind:0,U:0,F:0} —— 不会 panic，
// 但也无法与“真正的全零字段”区分。当需要区分时请用 AtOk（例如调用方提供的
// 本地模板字段数可能少于解包器预期的情况）。
func (w *Walk) At(i int) Value {
	if i < 0 || i >= len(w.vals) {
		return Value{}
	}
	return w.vals[i]
}

// AtOk 是带显式存在标志的 At：越界时 ok=false，调用方可以据此 fail-loud，
// 而不是静默读到一个零值。
func (w *Walk) AtOk(i int) (Value, bool) {
	if i < 0 || i >= len(w.vals) {
		return Value{}, false
	}
	return w.vals[i], true
}

// ByOctet 返回起始于基于 1 的 section octet 号的字段取值。若没有固定位置的
// 字段在那里起始（symbolic 位置，或落在某个多字节字段内部），ok=false。
func (w *Walk) ByOctet(octet int) (Value, bool) {
	for i := range w.def.Fields {
		if f := w.def.Fields[i]; f.Start >= 0 && f.Start+1 == octet {
			return w.vals[i], true
		}
	}
	return Value{}, false
}

// SymbolicAt 返回第一个 Symbolic 字段的索引，没有则返回 -1。
func (w *Walk) SymbolicAt() int {
	for i, f := range w.def.Fields {
		if f.Kind == KindSymbolic {
			return i
		}
	}
	return -1
}

// Tail 返回从基于 1 的 section octet（fromOctet）到 section 末尾的原始字节
// （变长尾部）。越界输入返回 nil。
func (w *Walk) Tail(fromOctet int) []byte {
	off := fromOctet - 1
	if off < 0 || off > len(w.data) {
		return nil
	}
	return w.data[off:]
}

// Data 返回该 walk 所基于的完整 section 字节。
func (w *Walk) Data() []byte { return w.data }

// Scale 按 WMO/eccodes 对“缩放值”的约定进行换算：
//
//	scale >= 0 → raw / 10^scale
//	scale <  0 → raw * 10^(-scale)
//
// 与 ecCodes 的 FromScaleFactorScaledValue 访问器一致
// （“real_value = scaled_value / pow(10, scale_factor)”）。
//
// 这里保留完整的带符号取值范围：GRIB2 的 scale factor 是带符号 1 字节字段，
// |scale| 可达 127，10^±127 仍是有限 float64 —— 对 |scale| 做任何静默钳制
// 都会让使用更大因子的文件算出错误数值且无报错。
func Scale(raw uint32, scale int) float64 {
	if scale >= 0 {
		return float64(raw) / math.Pow(10, float64(scale))
	}
	return float64(raw) * math.Pow(10, float64(-scale))
}

// Signed8 用符号-幅度方式解码 GRIB2 的 1 字节带符号字段：bit 7 为符号位，
// 低 7 位为幅值。
//
// 这是 ecCodes 每个 `signed[N]` 访问器的约定 —— 见 grib_bits.cc:grib_decode_signed_long，
// 它先屏蔽符号位、把剩余位当幅值读出再取负。本项目针对 signed[4] 的网格角度与
// signed[2] 的打包 scale factor E/D，已用 ecCodes/g2c/wgrib2 实证过同一约定
// 。
//
// 注意 0xFF 解码为 -127，这正是 ecCodes 对 1 字节带符号字段的“缺失”哨兵值
// （Signed.cc 的 `ones[] = {0, -0x7f, ...}`）；能取缺失值的调用方必须先判定再使用。
func Signed8(b byte) int {
	if b&0x80 != 0 {
		return -int(b & 0x7F)
	}
	return int(b)
}

// Signed16 用符号-幅度方式解码 GRIB2 的 2 字节带符号字段（bit 15 为符号位，
// 低 15 位为幅值）。证据与缺失值注意点见 Signed8（0xFFFF 解码为 -32767）。
func Signed16(u uint16) int {
	if u&0x8000 != 0 {
		return -int(u & 0x7FFF)
	}
	return int(u)
}

// Signed32 用符号-幅度方式解码 GRIB2 的 4 字节带符号字段（bit 31 为符号位，
// 低 31 位为幅值）。见 Signed8。
func Signed32(u uint32) int {
	if u&0x80000000 != 0 {
		return -int(u & 0x7FFFFFFF)
	}
	return int(u)
}
