//
// JPEG 2000（GRIB2 数据表示模板 5.40）解码器子集。
//
// 范围刻意收窄并坚持 fail-loud（“解码器从不猜测”）：5/3 可逆 +
// 无量化（无损）路径、单分量、单 tile、LRCP 递进顺序、无 precinct code block、
// 无 ROI、无 code-block 模式、无 SOP/EPH marker。凡超出该子集的一律返回错误，
// 而不是给出看似合理的猜测。
//
// 各 marker 段的字段布局遵循 Jasper 码流方言，这也正是 CMA TRAMS/GRAPES 语料
// 所采用的（其 COM 注释写着 “Creator: Jasper Version 1.9000.1”，eccodes 通过
// libjasper 解码它们）。SIZ 的布局与 ISO/IEC 15444-1 一致（Rsiz/Xsiz/.../Csiz，
// tile 尺寸 4 字节，Csiz 2 字节 —— OpenJPEG 读取的宽度相同）；但 COD 与 SOT
// 不一致：Jasper 写 COD 时不带 SGcod(A) 标志字节，并带一个显式的分解级数字节，
// 而 SOT 写作 tileno(2)/Psot(4)/partno(1)/numparts(1)。不匹配该方言的码流会被
// 拒绝，而不会被重新解释。
//
// 包头遵循 Jasper 使用的 tag-tree 编码（T.800 附录 G 风格：二进制算术编码的
// inclusion/numimsbs tag tree、逗号码、定长码），而非附录 B 的 MQ Lblock/Lcode/
// Ltag 编码。这是输入语料的固有属性，并且会被校验：无法解码的包头即为错误。

package pack

import (
	"encoding/binary"
	"fmt"
)

// JPEG 2000 marker 码（T.800 表 A-1）。
const (
	jpcSOC = 0xff4f // 码流开始
	jpcSIZ = 0xff51 // 图像与 tile 尺寸
	jpcCOD = 0xff52 // 默认编码风格
	jpcCOC = 0xff53 // 分量编码风格
	jpcQCD = 0xff5c // 默认量化
	jpcQCC = 0xff5d // 分量量化
	jpcRGN = 0xff5e // 感兴趣区域
	jpcCOM = 0xff64 // 注释
	jpcSOT = 0xff90 // tile-part 开始
	jpcSOP = 0xff91 // 包开始
	jpcEPH = 0xff92 // 包头结束
	jpcSOD = 0xff93 // 数据开始
	jpcEOC = 0xffd9 // 码流结束
)

// Scod 位（编码风格标志），命名与参考实现保持一致。
const (
	jpcCoxPrt = 0x01 // 使用 precinct code block
	jpcCodSop = 0x02 // 使用 SOP marker 段
	jpcCodEph = 0x04 // 使用 EPH marker 段
	jpcCoxEpc = 0x08 // 逐编码通道显式给出 precinct code block
)

// ScodM 位（code-block 模式），命名与参考实现保持一致。bypass 编码即 LAZY；
// 其余模式为 RESET、TERMALL、VSC、PTERM、SEGSYM。
const (
	jpcCoxLazy    = 0x01 // 选择性算术编码 bypass
	jpcCoxReset   = 0x02 // 重置上下文概率
	jpcCoxTermall = 0x04 // 终止所有编码通道
	jpcCoxVsc     = 0x08 // 垂直条带因果上下文形成
	jpcCoxPterm   = 0x10 // 可预测终止
	jpcCoxSegsym  = 0x20 // 分段符号

	jpcQcxNoqnt = 0 // 无量化
)

// 变换标识（COD/QCD 的 “Transform” 字节）。
const (
	jpcCoxIns = 0x00 // 不可逆 9/7
	jpcCoxRft = 0x01 // 可逆 5/3
)

// ErrJPCUnsupported 报告某项超出已实现子集的 JPEG 2000 特性。它按设计
// fail-loud：解码器从不猜测。
type ErrJPCUnsupported struct {
	Feature string
	Detail  string
}

func (e *ErrJPCUnsupported) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("pack: jpeg2000: unsupported feature: %s", e.Feature)
	}
	return fmt.Sprintf("pack: jpeg2000: unsupported feature: %s (%s)", e.Feature, e.Detail)
}

// ErrJPCMalformed 报告无法按 Jasper 方言的 JPEG 2000 码流解析的码流。
type ErrJPCMalformed struct{ Reason string }

func (e *ErrJPCMalformed) Error() string {
	return fmt.Sprintf("pack: jpeg2000: malformed codestream: %s", e.Reason)
}

// jpcComp 是一个 SIZ 分量描述符。
type jpcComp struct {
	prec  int  // 分量位深
	sgnd  int  // 0 = 无符号, 1 = 有符号
	hsamp int  // 水平间隔 (XRsiz)
	vsamp int  // 垂直间隔 (YRsiz)
	raw   byte // 原始描述符字节，用于错误消息
}

// jpcCodingStyle 是 COD（或 COC）编码风格参数，按 Jasper 的读取方式。
type jpcCodingStyle struct {
	csty      int
	prog      int   // 递进顺序 (0 = LRCP)
	numlyrs   int   // layer 数量
	mct       int   // 多分量变换
	numdlvls  int   // 分解级数
	cblkWexpn int   // code block 宽度指数
	cblkHexpn int   // code block 高度指数
	cblksty   int   // code block 模式
	qmfbid    int   // 0 = 不可逆 9/7, 1 = 可逆 5/3
	prcWexpn  []int // 每级的 precinct 宽度指数（无 precinct 时为 0）
	prcHexpn  []int
}

// jpcQuant 是 QCD/QCX 量化参数，按 Jasper 的读取方式。
type jpcQuant struct {
	qntsty   int
	numguard int
	expns    []int // 逐 band 的指数（NOQNT 下该字节即指数）
	mants    []int // SEQNT 下逐 band 的尾数（11 位）
}

// expnAt 返回第 i 个 band 的指数；标量派生量化（style 1）只存一个步长，
// 作用于所有 band。
func (q jpcQuant) expnAt(i int) int {
	if len(q.expns) == 1 {
		return q.expns[0]
	}
	return q.expns[i]
}

// mantAt 返回第 i 个 band 的尾数；没有尾数或索引超出声明列表时按 0 处理。
func (q jpcQuant) mantAt(i int) int {
	if len(q.mants) == 1 {
		return q.mants[0]
	}
	if i < len(q.mants) {
		return q.mants[i]
	}
	return 0
}

// jpcImage 是解析后的 JPEG 2000 码流，且限定在受支持的子集内。
type jpcImage struct {
	xsiz, ysiz     int
	xosiz, yosiz   int
	xtsiz, ytsiz   int
	xt0siz, yt0siz int
	comps          []jpcComp

	cs       jpcCodingStyle
	csSet    bool
	quant    jpcQuant
	quantSet bool

	// tileData 是唯一 tile 的 tile-part 数据（SOD 与下一个 marker 之间的全部内容）。
	tileData []byte
	comment  string
}

// parseJPC 将 data 解析为 Jasper 方言的 JPEG 2000 码流，并拒绝任何超出受支持
// 无损 5/3 子集的内容。
func parseJPC(data []byte) (*jpcImage, error) {
	if len(data) < 4 {
		return nil, &ErrJPCMalformed{Reason: "codestream shorter than 4 bytes"}
	}
	p := 0
	if binary.BigEndian.Uint16(data[0:2]) == jpcSOC {
		p = 2
	}

	img := &jpcImage{}
	var sizSeen, codSeen, qcdSeen, sotSeen, sodSeen bool
	var tileParts int
	var tileDataStart = -1
	var tileDataEnd = -1
	var sotStart = -1
	var psot int

	for p < len(data) {
		if data[p] != 0xff {
			if !sodSeen {
				return nil, &ErrJPCMalformed{Reason: fmt.Sprintf("non-marker byte %02x at offset %d before SOD", data[p], p)}
			}
			// tile 数据由 Psot 界定，因此其后出现非 marker 前缀的字节意味着码流
			// 不一致；编码数据从不用于扫描 marker。
			return nil, &ErrJPCMalformed{Reason: fmt.Sprintf("non-marker byte %02x at offset %d after the tile data", data[p], p)}
		}
		markerStart := p
		for p < len(data) && data[p] == 0xff {
			p++
		}
		if p >= len(data) {
			break
		}
		mark := int(data[p]) | 0xff00
		p++
		if mark == 0xff00 || mark == 0xff4f || mark == 0xff50 || mark == 0xffff {
			continue // 填充或保留
		}

		if mark == jpcSOD || mark == jpcEOC {
			switch mark {
			case jpcSOD:
				if !sotSeen {
					return nil, &ErrJPCMalformed{Reason: "SOD without SOT"}
				}
				sodSeen = true
				tileDataStart = p
				// SOT 的 Psot 界定了 tile 数据：编码后的 tile 合法地包含
				// 0xFF 后跟非零字节（MQ 字节填充），因此绝不能对它扫描 marker。
				// 读取 SOT 时 Psot == 0 会被拒绝。
				tileDataEnd = sotStart + psot
				if tileDataEnd > len(data) {
					return nil, &ErrJPCMalformed{Reason: fmt.Sprintf("SOT declares Psot=%d beyond the codestream (%d bytes)", psot, len(data))}
				}
				p = tileDataEnd
			default:
				if !sodSeen {
					return nil, &ErrJPCMalformed{Reason: "EOC without SOD"}
				}
				if tileDataEnd < 0 {
					tileDataEnd = markerStart
				}
				return finishJPC(img, data, tileDataStart, tileDataEnd)
			}
			continue
		}

		if p+2 > len(data) {
			return nil, &ErrJPCMalformed{Reason: fmt.Sprintf("truncated marker %04x at offset %d", mark, p-2)}
		}
		l := int(binary.BigEndian.Uint16(data[p : p+2]))
		if l < 2 || p+l > len(data) {
			return nil, &ErrJPCMalformed{Reason: fmt.Sprintf("marker %04x at offset %d declares length %d, codestream has %d bytes", mark, p-2, l, len(data))}
		}
		payload := data[p+2 : p+l]
		p += l

		switch mark {
		case jpcSIZ:
			if sizSeen {
				return nil, &ErrJPCMalformed{Reason: "duplicate SIZ"}
			}
			if err := parseSIZ(payload, img); err != nil {
				return nil, err
			}
			sizSeen = true
		case jpcCOD:
			if codSeen {
				return nil, &ErrJPCUnsupported{Feature: "multiple COD marker segments"}
			}
			if err := parseCOD(payload, img); err != nil {
				return nil, err
			}
			codSeen = true
		case jpcQCD:
			if qcdSeen {
				return nil, &ErrJPCUnsupported{Feature: "multiple QCD marker segments"}
			}
			q, err := parseQCX(payload)
			if err != nil {
				return nil, err
			}
			img.quant = q
			img.quantSet = true
			qcdSeen = true
		case jpcQCC:
			// 逐分量量化；Jasper 对某些图像会在 tile-part 头里输出它。只能寻址
			// 唯一受支持的那个分量。
			if len(payload) < 2 {
				return nil, &ErrJPCMalformed{Reason: "QCC payload too short"}
			}
			if int(payload[0]) != 0 {
				return nil, &ErrJPCUnsupported{Feature: "per-component quantization for a component other than the first", Detail: fmt.Sprintf("compno=%d", payload[0])}
			}
			q, err := parseQCX(payload[1:])
			if err != nil {
				return nil, err
			}
			img.quant = q
			img.quantSet = true
		case jpcCOM:
			img.comment = parseCOM(payload)
		case jpcSOT:
			if len(payload) < 8 {
				return nil, &ErrJPCMalformed{Reason: "SOT shorter than 8 bytes"}
			}
			tileno := int(binary.BigEndian.Uint16(payload[0:2]))
			psot = int(binary.BigEndian.Uint32(payload[2:6]))
			partno := int(payload[6])
			numparts := int(payload[7])
			if tileno != 0 || partno > 0 {
				return nil, &ErrJPCUnsupported{
					Feature: "multi-tile or multi-tile-part codestream",
					Detail:  fmt.Sprintf("tileno=%d partno=%d", tileno, partno),
				}
			}
			// 尽管只接受单 part 的码流，声明的 part 总数仍必须与 part 编号自洽。
			if numparts < 1 || partno >= numparts {
				return nil, &ErrJPCMalformed{Reason: fmt.Sprintf("SOT declares partno=%d of numparts=%d", partno, numparts)}
			}
			// 没有 Psot 就定位 tile 数据意味着要扫描编码数据找 marker，而编码
			// 数据合法地包含 0xFF 后跟非零字节（MQ 字节填充），因此长度为 0 时
			// 直接拒绝而非猜测。
			if psot == 0 {
				return nil, &ErrJPCUnsupported{
					Feature: "tile-part with Psot = 0",
					Detail:  "the tile data extent cannot be determined without the tile-part length",
				}
			}
			tileParts++
			if tileParts > 1 {
				return nil, &ErrJPCUnsupported{Feature: "multiple tile-parts"}
			}
			sotStart = markerStart
			sotSeen = true
		default:
			return nil, &ErrJPCUnsupported{
				Feature: fmt.Sprintf("marker %04x", mark),
				Detail:  "COC/QCC/RGN/POC/PPM/PPT/SOP/EPH and other marker segments are not implemented",
			}
		}
	}
	return nil, &ErrJPCMalformed{Reason: "codestream ended without EOC"}
}

// finishJPC 校验解析出的头部并挂上 tile 数据。
func finishJPC(img *jpcImage, data []byte, dataStart, dataEnd int) (*jpcImage, error) {
	if img.xsiz == 0 || img.ysiz == 0 {
		return nil, &ErrJPCMalformed{Reason: "SIZ missing"}
	}
	if !img.csSet {
		return nil, &ErrJPCMalformed{Reason: "COD missing"}
	}
	if !img.quantSet {
		return nil, &ErrJPCMalformed{Reason: "QCD missing"}
	}
	if dataStart < 0 || dataEnd <= dataStart {
		return nil, &ErrJPCMalformed{Reason: "empty tile data"}
	}
	img.tileData = data[dataStart:dataEnd]
	return img, nil
}

func parseCOM(payload []byte) string {
	if len(payload) < 1 {
		return ""
	}
	body := payload[1:]
	if len(body) >= 2 {
		body = body[2:] // 语言代码 + 字符集
	}
	return string(body)
}

func parseSIZ(payload []byte, img *jpcImage) error {
	if len(payload) < 36 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("SIZ payload %d bytes, need at least 36", len(payload))}
	}
	// caps(2) Xsiz(4) Ysiz(4) XOsiz(4) YOsiz(4) XTsiz(4) YTsiz(4)
	// XT0siz(4) YT0siz(4) Csiz(2)
	img.xsiz = int(binary.BigEndian.Uint32(payload[2:6]))
	img.ysiz = int(binary.BigEndian.Uint32(payload[6:10]))
	img.xosiz = int(binary.BigEndian.Uint32(payload[10:14]))
	img.yosiz = int(binary.BigEndian.Uint32(payload[14:18]))
	img.xtsiz = int(binary.BigEndian.Uint32(payload[18:22]))
	img.ytsiz = int(binary.BigEndian.Uint32(payload[22:26]))
	img.xt0siz = int(binary.BigEndian.Uint32(payload[26:30]))
	img.yt0siz = int(binary.BigEndian.Uint32(payload[30:34]))
	// SIZ 几何合理性校验（ISO/IEC 15444-1 §A.5.1）。Xsiz/Ysiz 是图像边界，
	// XOsiz/YOsiz 是图像原点，XTsiz/YTsiz 是 tile 尺寸，XTOsiz/YTOsiz 是
	// tile 网格原点。tile 尺寸是 jpcDecodeSamples 中 tile 计数运算的除数，
	// 因此它为 0 就等于埋下一处整数除零（bug.md §1.5）；原点约束则防止同一
	// 运算产生负数计数。这些都是纯文件数据：在任何除法之前先校验。
	if img.xtsiz <= 0 || img.ytsiz <= 0 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("SIZ tile size %dx%d must be positive", img.xtsiz, img.ytsiz)}
	}
	if img.xosiz > img.xsiz || img.yosiz > img.ysiz {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("SIZ image origin (%d,%d) outside the image (%d,%d)", img.xosiz, img.yosiz, img.xsiz, img.ysiz)}
	}
	if img.xt0siz > img.xsiz || img.yt0siz > img.ysiz {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("SIZ tile origin (%d,%d) outside the image (%d,%d)", img.xt0siz, img.yt0siz, img.xsiz, img.ysiz)}
	}
	ncomps := int(binary.BigEndian.Uint16(payload[34:36]))
	if ncomps != 1 {
		return &ErrJPCUnsupported{Feature: "multi-component codestream", Detail: fmt.Sprintf("Csiz=%d", ncomps)}
	}
	if len(payload) < 39 {
		return &ErrJPCMalformed{Reason: "SIZ component descriptor truncated"}
	}
	raw := payload[36]
	c := jpcComp{
		prec:  int(raw&0x7f) + 1, // Jasper：符号在位 7，precision-1 在低 7 位
		sgnd:  int(raw>>7) & 1,
		hsamp: int(payload[37]),
		vsamp: int(payload[38]),
		raw:   raw,
	}
	if c.hsamp != 1 || c.vsamp != 1 {
		return &ErrJPCUnsupported{Feature: "component subsampling", Detail: fmt.Sprintf("XRsiz=%d YRsiz=%d", c.hsamp, c.vsamp)}
	}
	if c.prec < 1 || c.prec > 31 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("component precision %d out of range", c.prec)}
	}
	if len(payload) != 39 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("SIZ payload has %d trailing bytes beyond one component descriptor (Scxoff/Scyoff present?)", len(payload)-39)}
	}
	img.comps = []jpcComp{c}
	return nil
}

func parseCOD(payload []byte, img *jpcImage) error {
	// Jasper 布局：Scod(1) progression(1) NLs(2) ScodM(1)，随后是编码风格参数：
	// numdlvls(1) cblkwidth(1) cblkheight(1) cblksty(1) qmfbid(1)，设置 precinct
	// 标志时再为每个分辨率级附加一个 precinct 字节。
	if len(payload) < 10 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("COD payload %d bytes, need at least 10 (Jasper dialect)", len(payload))}
	}
	cs := &img.cs
	cs.csty = int(payload[0])
	cs.prog = int(payload[1])
	cs.numlyrs = int(binary.BigEndian.Uint16(payload[2:4]))
	cs.mct = int(payload[4])
	cs.numdlvls = int(payload[5])
	cs.cblkWexpn = coxCblkSizeExpn(payload[6])
	cs.cblkHexpn = coxCblkSizeExpn(payload[7])
	cs.cblksty = int(payload[8])
	cs.qmfbid = int(payload[9])
	// 使用 precinct 时参考实现按分辨率级读取一个字节：低半字节是 precinct 宽度
	// 指数，高半字节是高度指数。这些半字节本身就是指数（不同于 code-block 尺寸
	// 字节，后者存放的是指数减二）。
	numrlvls := cs.numdlvls + 1
	if cs.csty&jpcCoxPrt != 0 {
		if len(payload) != 10+numrlvls {
			return &ErrJPCMalformed{Reason: fmt.Sprintf("COD payload %d bytes, precincts need 1 byte per resolution level (%d levels, so %d bytes)", len(payload), numrlvls, 10+numrlvls)}
		}
		cs.prcWexpn = make([]int, numrlvls)
		cs.prcHexpn = make([]int, numrlvls)
		for i := 0; i < numrlvls; i++ {
			cs.prcWexpn[i] = int(payload[10+i] & 0x0f)
			cs.prcHexpn[i] = int(payload[10+i] >> 4)
			if cs.prcWexpn[i] > 15 || cs.prcHexpn[i] > 15 {
				return &ErrJPCMalformed{Reason: fmt.Sprintf("precinct exponents %d/%d at level %d out of range", cs.prcWexpn[i], cs.prcHexpn[i], i)}
			}
		}
	} else if len(payload) != 10 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("COD payload %d bytes, expected 10 without precincts", len(payload))}
	}
	if cs.prog != 0 {
		return &ErrJPCUnsupported{Feature: "progression order", Detail: fmt.Sprintf("Sprg=%d (only LRCP=0 is implemented)", cs.prog)}
	}
	if cs.numlyrs != 1 {
		return &ErrJPCUnsupported{Feature: "multi-layer codestream", Detail: fmt.Sprintf("NLs=%d", cs.numlyrs)}
	}
	if cs.mct != 0 {
		return &ErrJPCUnsupported{Feature: "multi-component transform"}
	}
	if cs.qmfbid != jpcCoxRft && cs.qmfbid != jpcCoxIns {
		return &ErrJPCUnsupported{Feature: "wavelet transform", Detail: fmt.Sprintf("Transform=%d; only 5/3 (1) and 9/7 (0) are implemented", cs.qmfbid)}
	}
	if cs.cblksty != 0 {
		return &ErrJPCUnsupported{Feature: "code-block modes", Detail: fmt.Sprintf("ScodM=0x%02x (LAZY/RESET/TERMALL/VSC/PTERM/SEGSYM)", cs.cblksty)}
	}
	if cs.csty&(jpcCodSop|jpcCodEph|jpcCoxEpc) != 0 {
		return &ErrJPCUnsupported{
			Feature: "SOP/EPH marker segments or per-pass precinct code-block sets",
			Detail:  fmt.Sprintf("Scod=0x%02x", cs.csty),
		}
	}
	if cs.cblkWexpn < 2 || cs.cblkWexpn > 6 || cs.cblkHexpn < 2 || cs.cblkHexpn > 6 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("code block size exponents %d/%d out of range", cs.cblkWexpn, cs.cblkHexpn)}
	}
	if cs.numdlvls < 0 || cs.numdlvls > 32 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("number of decomposition levels %d out of range", cs.numdlvls)}
	}
	if cs.csty&(0x02|0x04|0x08) != 0 {
		return &ErrJPCUnsupported{Feature: "SOP/EPH markers or selective arithmetic coding", Detail: fmt.Sprintf("Scod=0x%02x", cs.csty)}
	}
	img.csSet = true
	return nil
}

// coxCblkSizeExpn 解码 Jasper 的 code-block 尺寸字节。Jasper 在 COD marker 段中
// 存放的是“指数减二”（JPC_COX_GETCBLKSIZEEXPN 就是 x+2），因此值为 4 表示
// 2^6 = 64 宽的 code block。这是 JasPer 方言的细节，而非该字段的 ISO 编码。
func coxCblkSizeExpn(v byte) int {
	return int(v) + 2
}

func parseQCX(payload []byte) (jpcQuant, error) {
	var q jpcQuant
	if len(payload) < 1 {
		return q, &ErrJPCMalformed{Reason: "empty QCD payload"}
	}
	q.qntsty = int(payload[0] & 0x1f)
	q.numguard = int(payload[0]>>5) & 7
	readStep := func(b []byte) (expn, mant int, err error) {
		if len(b) < 2 {
			return 0, 0, &ErrJPCMalformed{Reason: "short QCD step size"}
		}
		v := int(b[0])<<8 | int(b[1])
		return (v >> 11) & 0x1f, v & 0x7ff, nil
	}
	switch q.qntsty {
	case jpcQcxNoqnt:
		// 对于无量化风格，Jasper 每个子带读取一个字节，且只保留指数。
		n := len(payload) - 1
		if n > 3*33+1 {
			return q, &ErrJPCMalformed{Reason: fmt.Sprintf("QCD declares %d step sizes, too many", n)}
		}
		q.expns = make([]int, n)
		for i := 0; i < n; i++ {
			q.expns[i] = int(payload[1+i] >> 3)
		}
	case 1: // 标量派生：一个步长作用于所有 band
		expn, mant, err := readStep(payload[1:])
		if err != nil {
			return q, err
		}
		q.expns = []int{expn}
		q.mants = []int{mant}
	case 2: // 标量显式：逐 band 两个字节
		n := (len(payload) - 1) / 2
		if (len(payload)-1)%2 != 0 {
			return q, &ErrJPCMalformed{Reason: "QCD has an odd number of step-size bytes"}
		}
		q.expns = make([]int, n)
		q.mants = make([]int, n)
		for i := 0; i < n; i++ {
			expn, mant, err := readStep(payload[1+2*i:])
			if err != nil {
				return q, err
			}
			q.expns[i], q.mants[i] = expn, mant
		}
	default:
		return q, &ErrJPCUnsupported{Feature: "quantization", Detail: fmt.Sprintf("Sqcd style=%d", q.qntsty)}
	}
	return q, nil
}
