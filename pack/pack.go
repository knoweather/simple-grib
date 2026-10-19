package pack

import (
	"errors"
	"fmt"

	"github.com/knoweather/simple-grib/internal/octetio"
	"github.com/knoweather/simple-grib/spec"
)

// ErrUnsupported 由 Unpack 返回：该数据表示模板号没有注册任何解码器。
//
// ErrUnsupported is returned by Unpack for data-representation template
// numbers that have no registered decoder.
var ErrUnsupported = errors.New("pack: unsupported data representation template")

// ErrDecoderNotConfigured 由占位解码器返回：该编解码器需要外部库
// （历史上的 JPEG 2000 = 40、WebP = 49152）——模板被识别，但没有接入内置
// 编解码器。可通过 Register 在运行时安装真正的解码器。
//
// ErrDecoderNotConfigured is returned by placeholder decoders for codecs
// that need an external library (JPEG 2000 = 40, WebP = 49152 in the past):
// the template is recognized but no built-in codec is wired up. A real
// decoder can be installed at runtime via Register.
type ErrDecoderNotConfigured struct {
	Template int
	Codec    string
}

func (e *ErrDecoderNotConfigured) Error() string {
	return fmt.Sprintf("pack: template %d (%s): decoder not configured; install one via pack.Register", e.Template, e.Codec)
}

// Unpacker 为某个数据表示模板解码第 7 段的负载。
//
// Unpacker decodes the section 7 payload for one data-representation
// template.
type Unpacker interface {
	Name() string
	Unpack(ctx *Context) ([]float32, error)
}

// Context 携带 Unpacker 所需的全部输入：第 5 段已解析的模板字段（Walk）、
// 调用方切出的第 7 段负载（自第 6 八位组起）、应用第 6 段位图后应有的输出
// 长度，以及该可选位图。NumValues 恒为解码器必须产出的值数——有位图时它等于
// 前 GridPoints 位的置位计数（由调用方校验），无位图时即网格点总数。
//
// Context carries everything an Unpacker needs: the parsed template fields
// of section 5 (Walk), the section 7 payload as cut by the caller (from
// octet 6 onward), the expected output length after the section 6 bitmap is
// applied, and that optional bitmap. NumValues is always the number of
// values the decoder must produce — with a bitmap it equals the popcount of
// the first GridPoints bits (verified by the caller), without a bitmap it is
// the full grid-point count.
type Context struct {
	// TemplateNumber：第 5 段第 10-11 八位组 / section 5 octets 10-11
	TemplateNumber int
	// Fields：第 5 段模板字段的解析结果（nil = 无 spec 定义）/ parsed section 5 template fields (nil = no spec definition)
	Fields *spec.Walk
	// Data：第 7 段负载（自第 6 八位组起）/ section 7 payload (from octet 6 onward)
	Data []byte
	// NumValues：应用位图后的预期输出长度 / expected output length after the bitmap is applied
	NumValues int
	// Bitmap：nil = 无位图；第 i 位为 1 表示网格点 i 有效 / nil = no bitmap; bit i=1 means grid point i is valid
	Bitmap *octetio.Bits
	// GridPoints：网格点总数（位图宽度基准）/ total number of grid points (bitmap width baseline)
	GridPoints int
}

// registry 把数据表示模板号映射到解码器。内置条目在 init 中注册；
// Register 会覆盖任何已有条目。
//
// 注意：该 map 是全局可变状态，并发不安全。在 Unpack 运行期间调用 Register
// 会造成数据竞争；只应在启动阶段（init 或单线程初始化）注册。
//
// registry maps data-representation template numbers to decoders. Built-in
// entries are registered in init; Register overwrites any existing entry.
//
// Note: the map is global mutable state and not concurrent-safe. Calling
// Register while an Unpack is running is a data race; register only during
// startup (init or single-threaded initialization).
var registry = map[int]Unpacker{}

func init() {
	Register(TemplateSimple, simpleUnpacker{})
	Register(TemplateIEEE, ieeeUnpacker{})
	Register(TemplateComplex, complexUnpacker{})
	Register(TemplateComplexPlain, complexPlainUnpacker{})
	Register(TemplateCCSDS, ccsdsUnpacker{})
	Register(TemplateJPEG2000, jpcUnpacker{})
	Register(TemplateWebP, placeholderUnpacker{codec: "WebP lossless"})
}

// Register 为某个数据表示模板号安装（或替换）解码器。
//
// Register installs (or replaces) the decoder for a data representation
// template number.
func Register(templateNumber int, u Unpacker) {
	registry[templateNumber] = u
}

// Unpack 把 ctx 分派给 ctx.TemplateNumber 对应的已注册解码器。
// 未知模板返回包裹了模板号的 ErrUnsupported。
//
// Unpack dispatches ctx to the registered decoder for ctx.TemplateNumber.
// Unknown templates produce a wrapped ErrUnsupported carrying the template
// number.
func Unpack(ctx *Context) ([]float32, error) {
	u, ok := registry[ctx.TemplateNumber]
	if !ok {
		return nil, fmt.Errorf("%w %d", ErrUnsupported, ctx.TemplateNumber)
	}
	return u.Unpack(ctx)
}

// simpleUnpacker 解码模板 5.0（网格点数据 - 简单打包）。R/E/D/bits/origType
// 一律从第 5 段 Walk 读取（字段序按生成的 spec 表：0=R F32，1=E U16，
// 2=D U16，3=bits U8，4=origType U8），绝不依赖硬编码假设。
//
// simpleUnpacker decodes template 5.0 (grid point data - simple packing).
// R/E/D/bits/origType are read from the section 5 Walk (field order per the
// generated spec table: 0=R F32, 1=E U16, 2=D U16, 3=bits U8, 4=origType
// U8) — never from hardcoded assumptions.
type simpleUnpacker struct{}

func (simpleUnpacker) Name() string { return "simple packing (5.0)" }

func (simpleUnpacker) Unpack(ctx *Context) ([]float32, error) {
	if ctx.Fields == nil || ctx.Fields.Len() < 5 {
		return nil, fmt.Errorf("pack: template 5.0: section 5 fields missing or truncated (walk len %d)", walkLen(ctx.Fields))
	}
	R := ctx.Fields.At(0).F                         // 第 12-15 八位组，已按 10^D 预缩放 / octets 12-15, pre-scaled by 10^D
	E := int16(smInt16(uint16(ctx.Fields.At(1).U))) // 第 16-17 八位组，符号幅值 / octets 16-17, sign-magnitude
	D := int16(smInt16(uint16(ctx.Fields.At(2).U))) // 第 18-19 八位组，符号幅值 / octets 18-19, sign-magnitude
	bits := uint8(ctx.Fields.At(3).U)               // 第 20 八位组 / octet 20
	origType := uint8(ctx.Fields.At(4).U)           // 第 21 八位组（表 5.1）/ octet 21 (table 5.1)

	return simpleDecode(SimplePacking{R: R, E: E, D: D, Bits: bits, OrigType: origType}, ctx.Data, ctx.NumValues)
}

// smInt16 解码 GRIB2 符号幅值 int16（bit 15 = 符号，低 15 位 = 幅值；
// 0x8000 = -0，按 0 处理）。这是第 5 段 E/D 字段的生态约定——不是补码。
// 依据：真实 ECMWF 文件把 E=-6 存为 0x8006，ecCodes 的 grib_set 把 D=-2
// 写成 0x8002（补码应为 0xFFFE）。
//
// smInt16 decodes a GRIB2 sign-magnitude int16 (bit 15 = sign, low 15 bits =
// magnitude; 0x8000 = -0, treated as 0). This is the ecosystem convention for
// the section 5 E/D fields — NOT two's complement. Evidence: real ECMWF files store E=-6 as 0x8006 and
// eccodes grib_set writes D=-2 as 0x8002 (two's complement would be 0xFFFE).
func smInt16(raw uint16) int {
	if raw&0x8000 != 0 {
		return -int(raw & 0x7FFF)
	}
	return int(raw)
}

// ieeeUnpacker 解码模板 5.4（IEEE 浮点数据），仅精度 1（binary32）；
// 精度从第 5 段 Walk（字段 0）读取。
//
// ieeeUnpacker decodes template 5.4 (IEEE floating point data), precision 1
// (binary32) only; precision is read from the section 5 Walk (field 0).
type ieeeUnpacker struct{}

func (ieeeUnpacker) Name() string { return "IEEE floating point (5.4)" }

func (ieeeUnpacker) Unpack(ctx *Context) ([]float32, error) {
	if ctx.Fields == nil || ctx.Fields.Len() < 1 {
		return nil, fmt.Errorf("pack: template 5.4: section 5 fields missing or truncated (walk len %d)", walkLen(ctx.Fields))
	}
	precision := uint8(ctx.Fields.At(0).U) // 第 12 八位组（表 5.7）/ octet 12 (table 5.7)
	return ieeeDecode(IEEE{Precision: precision}, ctx.Data, ctx.NumValues)
}

// placeholderUnpacker 代表需要外部库的编解码器：模板被识别，
// 但没有内置解码器。
//
// placeholderUnpacker stands in for codecs that need an external library:
// the template is recognized, but there is no built-in decoder.
type placeholderUnpacker struct {
	codec string
}

func (p placeholderUnpacker) Name() string { return p.codec + " (placeholder)" }

func (p placeholderUnpacker) Unpack(ctx *Context) ([]float32, error) {
	return nil, &ErrDecoderNotConfigured{Template: ctx.TemplateNumber, Codec: p.codec}
}

// walkLen 返回 Walk 的字段数；w 为 nil 时返回 0。
//
// walkLen returns the number of fields in w, 0 for a nil Walk.
func walkLen(w *spec.Walk) int {
	if w == nil {
		return 0
	}
	return w.Len()
}
