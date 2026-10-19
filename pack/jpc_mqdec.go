//
// 面向 JPEG 2000 码块段的 MQ (QM) 算术解码器（T.800 Annex C）。47 区间状态表
// 与 A/C/CT 寄存器算术严格遵循标准；初始化序列（先载入两个字节，再把 C 左移
// 7 位，A = 0x8000）以及 0xFF 字节填充规则，都与参考读取器处理 CMA 语料时的
// 行为一致。

package pack

// mqState 是 MQ 状态表的一项：索引 = 2*interval + mps。
type mqState struct {
	qeval uint16
	mps   bool
	nmps  int // MPS 时的下一个状态索引
	nlps  int // LPS 时的下一个状态索引
}

// mqStates 是标准的 47 区间状态表，为两个 MPS 取值展开成双份。
var mqStates = [94]mqState{
	{0x5601, false, 2, 3}, {0x5601, true, 3, 2},
	{0x3401, false, 4, 12}, {0x3401, true, 5, 13},
	{0x1801, false, 6, 18}, {0x1801, true, 7, 19},
	{0x0ac1, false, 8, 24}, {0x0ac1, true, 9, 25},
	{0x0521, false, 10, 58}, {0x0521, true, 11, 59},
	{0x0221, false, 76, 66}, {0x0221, true, 77, 67},
	{0x5601, false, 14, 13}, {0x5601, true, 15, 12},
	{0x5401, false, 16, 28}, {0x5401, true, 17, 29},
	{0x4801, false, 18, 28}, {0x4801, true, 19, 29},
	{0x3801, false, 20, 28}, {0x3801, true, 21, 29},
	{0x3001, false, 22, 34}, {0x3001, true, 23, 35},
	{0x2401, false, 24, 36}, {0x2401, true, 25, 37},
	{0x1c01, false, 26, 40}, {0x1c01, true, 27, 41},
	{0x1601, false, 58, 42}, {0x1601, true, 59, 43},
	{0x5601, false, 30, 29}, {0x5601, true, 31, 28},
	{0x5401, false, 32, 28}, {0x5401, true, 33, 29},
	{0x5101, false, 34, 30}, {0x5101, true, 35, 31},
	{0x4801, false, 36, 32}, {0x4801, true, 37, 33},
	{0x3801, false, 38, 34}, {0x3801, true, 39, 35},
	{0x3401, false, 40, 36}, {0x3401, true, 41, 37},
	{0x3001, false, 42, 38}, {0x3001, true, 43, 39},
	{0x2801, false, 44, 38}, {0x2801, true, 45, 39},
	{0x2401, false, 46, 40}, {0x2401, true, 47, 41},
	{0x2201, false, 48, 42}, {0x2201, true, 49, 43},
	{0x1c01, false, 50, 44}, {0x1c01, true, 51, 45},
	{0x1801, false, 52, 46}, {0x1801, true, 53, 47},
	{0x1601, false, 54, 48}, {0x1601, true, 55, 49},
	{0x1401, false, 56, 50}, {0x1401, true, 57, 51},
	{0x1201, false, 58, 52}, {0x1201, true, 59, 53},
	{0x1101, false, 60, 54}, {0x1101, true, 61, 55},
	{0x0ac1, false, 62, 56}, {0x0ac1, true, 63, 57},
	{0x09c1, false, 64, 58}, {0x09c1, true, 65, 59},
	{0x08a1, false, 66, 60}, {0x08a1, true, 67, 61},
	{0x0521, false, 68, 62}, {0x0521, true, 69, 63},
	{0x0441, false, 70, 64}, {0x0441, true, 71, 65},
	{0x02a1, false, 72, 66}, {0x02a1, true, 73, 67},
	{0x0221, false, 74, 68}, {0x0221, true, 75, 69},
	{0x0141, false, 76, 70}, {0x0141, true, 77, 71},
	{0x0111, false, 78, 72}, {0x0111, true, 79, 73},
	{0x0085, false, 80, 74}, {0x0085, true, 81, 75},
	{0x0049, false, 82, 76}, {0x0049, true, 83, 77},
	{0x0025, false, 84, 78}, {0x0025, true, 85, 79},
	{0x0015, false, 86, 80}, {0x0015, true, 87, 81},
	{0x0009, false, 88, 82}, {0x0009, true, 89, 83},
	{0x0005, false, 90, 84}, {0x0005, true, 91, 85},
	{0x0001, false, 90, 86}, {0x0001, true, 91, 87},
	{0x5601, false, 92, 92}, {0x5601, true, 93, 93},
}

// mqCtxInit 是单个 MQ 上下文的初始状态。
type mqCtxInit struct {
	ind int
	mps bool
}

// Tier-1 上下文布局（Jasper 编号）：AGG(0)、ZC(1..9)、MAG(10..12)、
// SC(13..17)、U(18)。
const (
	jpcAggCtxNo = 0
	jpcZcCtxNo  = 1
	jpcMagCtxNo = 10
	jpcScCtxNo  = 13
	jpcUCtxNo   = 18
	jpcNumCtxs  = 19
)

// jpcMqCtxs 是码块解码所使用的初始上下文设置。
var jpcMqCtxs = [jpcNumCtxs]mqCtxInit{
	{ind: 3, mps: false},  // AGG
	{ind: 4, mps: false},  // ZC 0
	{ind: 0, mps: false},  // ZC 1
	{ind: 0, mps: false},  // ZC 2
	{ind: 0, mps: false},  // ZC 3
	{ind: 0, mps: false},  // ZC 4
	{ind: 0, mps: false},  // ZC 5
	{ind: 0, mps: false},  // ZC 6
	{ind: 0, mps: false},  // ZC 7
	{ind: 0, mps: false},  // ZC 8
	{ind: 0, mps: false},  // MAG 0
	{ind: 0, mps: false},  // MAG 1
	{ind: 0, mps: false},  // MAG 2
	{ind: 0, mps: false},  // SC 0
	{ind: 0, mps: false},  // SC 1
	{ind: 0, mps: false},  // SC 2
	{ind: 0, mps: false},  // SC 3
	{ind: 0, mps: false},  // SC 4
	{ind: 46, mps: false}, // U
}

// mqDecoder 从一个 MQ 编码的 segment 中解码比特。
type mqDecoder struct {
	data  []byte
	pos   int
	creg  uint32
	areg  uint32
	ctreg int
	ctx   []int // 每个上下文的状态索引
	cur   int
	prev  byte
	eof   bool
}

func newMQDecoder(data []byte) *mqDecoder {
	return &mqDecoder{data: data, ctx: make([]int, jpcNumCtxs)}
}

// resetContexts 恢复各上下文的初始概率。
func (d *mqDecoder) resetContexts() {
	for i, c := range jpcMqCtxs {
		d.ctx[i] = 2 * c.ind
		if c.mps {
			d.ctx[i]++
		}
	}
}

func (d *mqDecoder) setCurCtx(n int) { d.cur = n }

// init 载入 segment 的头两个字节（即 MQ 初始化）。
func (d *mqDecoder) init() {
	d.eof = false
	d.creg = 0
	c, ok := d.readByte()
	if !ok {
		c = 0xff
		d.eof = true
	}
	d.prev = c
	d.creg += uint32(c) << 16
	d.byteIn()
	d.creg <<= 7
	d.ctreg -= 7
	d.areg = 0x8000
}

func (d *mqDecoder) readByte() (byte, bool) {
	if d.pos >= len(d.data) {
		return 0, false
	}
	b := d.data[d.pos]
	d.pos++
	return b, true
}

// byteIn 即 BYTEIN 算法，包含 0xFF 字节填充规则。
func (d *mqDecoder) byteIn() {
	var c byte
	if !d.eof {
		var ok bool
		c, ok = d.readByte()
		if !ok {
			d.eof = true
			c = 0xff
		}
		prev := d.prev
		d.prev = c
		if prev == 0xff {
			if c > 0x8f {
				d.creg += 0xff00
				d.ctreg = 8
			} else {
				d.creg += uint32(c) << 9
				d.ctreg = 7
			}
		} else {
			d.creg += uint32(c) << 8
			d.ctreg = 8
		}
	} else {
		d.creg += 0xff00
		d.ctreg = 8
	}
}

func (d *mqDecoder) renormD() {
	for {
		if d.ctreg == 0 {
			d.byteIn()
		}
		d.areg <<= 1
		d.creg <<= 1
		d.ctreg--
		if d.areg&0x8000 != 0 {
			return
		}
	}
}

func (d *mqDecoder) mpsExchange(state mqState) bool {
	if d.areg < uint32(state.qeval) {
		d.ctx[d.cur] = state.nlps
		return !state.mps
	}
	d.ctx[d.cur] = state.nmps
	return state.mps
}

func (d *mqDecoder) lpsExchange(state mqState) bool {
	if d.areg >= uint32(state.qeval) {
		d.areg = uint32(state.qeval)
		d.ctx[d.cur] = state.nlps
		return !state.mps
	}
	d.areg = uint32(state.qeval)
	d.ctx[d.cur] = state.nmps
	return state.mps
}

// getbit 用当前上下文解码一个符号。
func (d *mqDecoder) getbit() bool {
	state := mqStates[d.ctx[d.cur]]
	d.areg -= uint32(state.qeval)
	if d.creg >= uint32(state.qeval)<<16 {
		d.creg -= uint32(state.qeval) << 16
		if d.areg&0x8000 != 0 {
			// A 仍保留其最高位的 1：此时解码出 MPS，
			// 既不做状态转移也不做重归一化。
			return state.mps
		}
		bit := d.mpsExchange(state)
		d.renormD()
		return bit
	}
	bit := d.lpsExchange(state)
	d.renormD()
	return bit
}
