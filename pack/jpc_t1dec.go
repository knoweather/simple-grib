//
// Tier-1（码块）解码：对一个 MQ 编码的 JPEG 2000 码块做 significance、
// refinement、cleanup 三个编码通道，采用参考读取器所用的四行 stripe/垂直
// 扫描结构以及上下文推导。码块模式（bypass、termination、reset、mode change、
// segmentation symbols、vertical causal context）在码流解析阶段就已被拒绝，
// 所以这里只出现普通的 MQ 路径。

package pack

// 每个采样点配套的编码标志，随码块采样一起存储。
const (
	jpcNESIG  = 0x0001
	jpcSESIG  = 0x0002
	jpcSWSIG  = 0x0004
	jpcNWSIG  = 0x0008
	jpcNSIG   = 0x0010
	jpcESIG   = 0x0020
	jpcSSIG   = 0x0040
	jpcWSIG   = 0x0080
	jpcNSGN   = 0x0100
	jpcESGN   = 0x0200
	jpcSSGN   = 0x0400
	jpcWSGN   = 0x0800
	jpcSIG    = 0x1000
	jpcREFINE = 0x2000
	jpcVISIT  = 0x4000
	jpcOTHSIG = jpcNSIG | jpcNESIG | jpcESIG | jpcSESIG | jpcSSIG | jpcSWSIG | jpcWSIG | jpcNWSIG
)

// 子带朝向。
const (
	jpcOrientLL = 0
	jpcOrientLH = 1
	jpcOrientHL = 2
	jpcOrientHH = 3
)

// zeroCodingContext 把邻域显著性模式映射到九个零编码上下文之一，
// 具体取决于子带朝向。
func zeroCodingContext(f int32, orient int) int {
	h := boolInt(f&jpcWSIG != 0) + boolInt(f&jpcESIG != 0)
	v := boolInt(f&jpcNSIG != 0) + boolInt(f&jpcSSIG != 0)
	d := boolInt(f&jpcNWSIG != 0) + boolInt(f&jpcNESIG != 0) +
		boolInt(f&jpcSESIG != 0) + boolInt(f&jpcSWSIG != 0)
	var n int
	switch orient {
	case jpcOrientHL:
		h, v = v, h
		fallthrough
	case jpcOrientLL, jpcOrientLH:
		switch {
		case h == 0:
			switch {
			case v == 0:
				if d == 0 {
					n = 0
				} else if d == 1 {
					n = 1
				} else {
					n = 2
				}
			case v == 1:
				n = 3
			default:
				n = 4
			}
		case h == 1:
			if v == 0 {
				if d == 0 {
					n = 5
				} else {
					n = 6
				}
			} else {
				n = 7
			}
		default:
			n = 8
		}
	case jpcOrientHH:
		hv := h + v
		switch {
		case d == 0:
			switch hv {
			case 0:
				n = 0
			case 1:
				n = 1
			default:
				n = 2
			}
		case d == 1:
			switch hv {
			case 0:
				n = 3
			case 1:
				n = 4
			default:
				n = 5
			}
		case d == 2:
			if hv == 0 {
				n = 6
			} else {
				n = 7
			}
		default:
			n = 8
		}
	default:
		n = 0
	}
	return jpcZcCtxNo + n
}

// signCount 返回（具有给定极性的显著邻居数）减去（显著且为负的邻居数），
// 用于符号预测。
func signCount(f int32, sigBit, sgnBit int32, otherSig, otherSgn int32) int {
	pos := boolInt(f&sigBit != 0 && f&sgnBit == 0) + boolInt(f&otherSig != 0 && f&otherSgn == 0)
	neg := boolInt(f&sigBit != 0 && f&sgnBit != 0) + boolInt(f&otherSig != 0 && f&otherSgn != 0)
	if pos > 1 {
		pos = 1
	}
	if neg > 1 {
		neg = 1
	}
	return pos - neg
}

func signCounts(f int32) (hc, vc int) {
	hc = signCount(f, jpcESIG, jpcESGN, jpcWSIG, jpcWSGN)
	vc = signCount(f, jpcNSIG, jpcNSGN, jpcSSIG, jpcSSGN)
	return hc, vc
}

// signPrediction 依据已显著的邻居，预测一个新变为显著采样点的符号。
func signPrediction(f int32) bool {
	hc, vc := signCounts(f)
	if hc == 0 && vc == 0 {
		return false
	}
	return !(hc > 0 || (hc == 0 && vc > 0))
}

// signCodingContext 把邻域显著性/符号模式映射到五个符号编码上下文之一。
func signCodingContext(f int32) int {
	hc, vc := signCounts(f)
	if hc < 0 {
		hc, vc = -hc, -vc
	}
	var n int
	if hc == 0 {
		if vc == -1 {
			n = 1
		} else if vc == 0 {
			n = 0
		} else {
			n = 1
		}
	} else {
		switch vc {
		case -1:
			n = 2
		case 0:
			n = 3
		default:
			n = 4
		}
	}
	return jpcScCtxNo + n
}

// magnitudeContext 返回被精化采样点的幅度上下文。
func magnitudeContext(f int32) int {
	var n int
	if f&jpcREFINE == 0 {
		if f&jpcOTHSIG != 0 {
			n = 1
		}
	} else {
		n = 2
	}
	return jpcMagCtxNo + n
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// updateFlags 把一个新变为显著的采样点记录到其邻居的状态中。
func updateFlags(flags []int32, fp, fstep int, negative, vcausal bool) {
	np := fp - fstep
	sp := fp + fstep
	if vcausal {
		flags[sp-1] |= jpcNESIG
		flags[sp+1] |= jpcNWSIG
		if negative {
			flags[sp] |= jpcNSIG | jpcNSGN
			flags[fp-1] |= jpcESIG | jpcESGN
			flags[fp+1] |= jpcWSIG | jpcWSGN
		} else {
			flags[sp] |= jpcNSIG
			flags[fp-1] |= jpcESIG
			flags[fp+1] |= jpcWSIG
		}
		return
	}
	flags[np-1] |= jpcSESIG
	flags[np+1] |= jpcSWSIG
	flags[sp-1] |= jpcNESIG
	flags[sp+1] |= jpcNWSIG
	if negative {
		flags[np] |= jpcSSIG | jpcSSGN
		flags[sp] |= jpcNSIG | jpcNSGN
		flags[fp-1] |= jpcESIG | jpcESGN
		flags[fp+1] |= jpcWSIG | jpcWSGN
	} else {
		flags[np] |= jpcSSIG
		flags[sp] |= jpcNSIG
		flags[fp-1] |= jpcESIG
		flags[fp+1] |= jpcWSIG
	}
}

// cblkPass 在解码各编码通道期间承载一个码块的状态。采样点存放在合成的分量
// 数组里（步长为 dstep）；标志位存放在一个专用的 (height+2) × (width+2) 矩阵中。
type cblkPass struct {
	flags  []int32
	fstep  int
	data   []int32
	dbase  int // 采样点 (0,0) 在 data 中的下标
	dstep  int
	width  int
	height int
}

func newCblkPass(width, height int, data []int32, dbase, dstep int) *cblkPass {
	return &cblkPass{
		flags:  make([]int32, (height+2)*(width+2)),
		fstep:  width + 2,
		data:   data,
		dbase:  dbase,
		dstep:  dstep,
		width:  width,
		height: height,
	}
}

// sigStep 解码 significance 通道中的一个采样点。
func (p *cblkPass) sigStep(mq *mqDecoder, fp, dp int, oneplushalf int32, orient int) {
	f := p.flags[fp]
	if f&jpcOTHSIG != 0 && f&(jpcSIG|jpcVISIT) == 0 {
		mq.setCurCtx(zeroCodingContext(f, orient))
		if mq.getbit() {
			mq.setCurCtx(signCodingContext(f))
			v := mq.getbit() != signPrediction(f)
			updateFlags(p.flags, fp, p.fstep, v, false)
			p.flags[fp] |= jpcSIG
			if v {
				p.data[dp] = -oneplushalf
			} else {
				p.data[dp] = oneplushalf
			}
		}
		p.flags[fp] |= jpcVISIT
	}
}

// sigpass 在整个码块上解码一个 significance 通道。
func (p *cblkPass) sigpass(mq *mqDecoder, bitpos, orient int) {
	one := int32(1) << bitpos
	oneplushalf := one | (one >> 1)
	for i := p.height; i > 0; i -= 4 {
		vscanlen := i
		if vscanlen > 4 {
			vscanlen = 4
		}
		stripe := p.height - i
		for j := p.width; j > 0; j-- {
			col := p.width - j
			fp := (1+stripe)*p.fstep + 1 + col
			dp := p.dbase + stripe*p.dstep + col
			k := vscanlen
			p.sigStep(mq, fp, dp, oneplushalf, orient)
			if k--; k <= 0 {
				continue
			}
			fp += p.fstep
			dp += p.dstep
			p.sigStep(mq, fp, dp, oneplushalf, orient)
			if k--; k <= 0 {
				continue
			}
			fp += p.fstep
			dp += p.dstep
			p.sigStep(mq, fp, dp, oneplushalf, orient)
			if k--; k <= 0 {
				continue
			}
			fp += p.fstep
			dp += p.dstep
			p.sigStep(mq, fp, dp, oneplushalf, orient)
		}
	}
}

// refStep 解码 refinement 通道中的一个采样点。
func (p *cblkPass) refStep(mq *mqDecoder, fp, dp int, poshalf, neghalf int32) {
	f := p.flags[fp]
	if f&(jpcSIG|jpcVISIT) == jpcSIG {
		mq.setCurCtx(magnitudeContext(f))
		v := mq.getbit()
		t := neghalf
		if v {
			t = poshalf
		}
		d := p.data[dp]
		if d < 0 {
			p.data[dp] = d - t
		} else {
			p.data[dp] = d + t
		}
		p.flags[fp] |= jpcREFINE
	}
}

// refpass 在整个码块上解码一个 refinement 通道。
func (p *cblkPass) refpass(mq *mqDecoder, bitpos int) {
	one := int32(1) << bitpos
	poshalf := one >> 1
	neghalf := int32(-1)
	if bitpos > 0 {
		neghalf = -poshalf
	}
	for i := p.height; i > 0; i -= 4 {
		vscanlen := i
		if vscanlen > 4 {
			vscanlen = 4
		}
		stripe := p.height - i
		for j := p.width; j > 0; j-- {
			col := p.width - j
			fp := (1+stripe)*p.fstep + 1 + col
			dp := p.dbase + stripe*p.dstep + col
			for k := 0; k < vscanlen; k++ {
				p.refStep(mq, fp, dp, poshalf, neghalf)
				fp += p.fstep
				dp += p.dstep
			}
		}
	}
}

// clnStep 解码 cleanup 通道中的一个采样点。skipZeroCoding 复现了参考实现的
// 游程捷径：在一次聚合游程之后，该游程的第一个采样点已知是显著的，
// 只需编码它的符号。
func (p *cblkPass) clnStep(mq *mqDecoder, fp, dp int, oneplushalf int32, orient int, skipZeroCoding bool) {
	f := p.flags[fp]
	if !skipZeroCoding {
		if f&(jpcSIG|jpcVISIT) == 0 {
			mq.setCurCtx(zeroCodingContext(f, orient))
			if mq.getbit() {
				p.clnSignificant(mq, fp, dp, f, oneplushalf)
			}
		}
	} else {
		p.clnSignificant(mq, fp, dp, f, oneplushalf)
	}
	p.flags[fp] &= ^int32(jpcVISIT)
}

func (p *cblkPass) clnSignificant(mq *mqDecoder, fp, dp int, f int32, oneplushalf int32) {
	mq.setCurCtx(signCodingContext(f))
	v := mq.getbit() != signPrediction(f)
	if v {
		p.data[dp] = -oneplushalf
	} else {
		p.data[dp] = oneplushalf
	}
	updateFlags(p.flags, fp, p.fstep, v, false)
	p.flags[fp] |= jpcSIG
}

// clnpass 解码一个 cleanup 通道，包括游程聚合。
func (p *cblkPass) clnpass(mq *mqDecoder, bitpos, orient int) {
	one := int32(1) << bitpos
	oneplushalf := one | (one >> 1)
	for i := 0; i < p.height; i += 4 {
		vscanlen := p.height - i
		if vscanlen > 4 {
			vscanlen = 4
		}
		for j := p.width; j > 0; j-- {
			col := p.width - j
			fbase := (1+i)*p.fstep + 1 + col
			dbase := p.dbase + i*p.dstep + col
			fp := fbase
			dp := dbase
			k := vscanlen
			aggregated := false
			if vscanlen >= 4 {
				clean := true
				idx := fbase
				for n := 0; n < 4; n++ {
					if p.flags[idx]&(jpcSIG|jpcVISIT|jpcOTHSIG) != 0 {
						clean = false
						break
					}
					idx += p.fstep
				}
				if clean {
					mq.setCurCtx(jpcAggCtxNo)
					if !mq.getbit() {
						continue
					}
					mq.setCurCtx(jpcUCtxNo)
					runlen := boolInt(mq.getbit())
					runlen = (runlen << 1) | boolInt(mq.getbit())
					fp = fbase + runlen*p.fstep
					dp = dbase + runlen*p.dstep
					k = vscanlen - runlen
					aggregated = true
				}
			}
			p.clnStep(mq, fp, dp, oneplushalf, orient, aggregated)
			if k--; k <= 0 {
				continue
			}
			fp += p.fstep
			dp += p.dstep
			p.clnStep(mq, fp, dp, oneplushalf, orient, false)
			if k--; k <= 0 {
				continue
			}
			fp += p.fstep
			dp += p.dstep
			p.clnStep(mq, fp, dp, oneplushalf, orient, false)
			if k--; k <= 0 {
				continue
			}
			fp += p.fstep
			dp += p.dstep
			p.clnStep(mq, fp, dp, oneplushalf, orient, false)
		}
	}
}
