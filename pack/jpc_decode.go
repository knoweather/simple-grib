//
// JPEG 2000 分块（tile）解码：code-block/precinct/分辨率级几何、包（packet）遍历
// （LRCP）、tier-2 包头解码、tier-1 code-block 解码，以及 5/3 逆离散小波变换，
// 最终按光栅顺序输出分量样本。
//
// 这里的几何构造完全复刻参考解码器的做法（分辨率级按最粗者在前排序，未使用
// precinct 时其指数默认取 15，code-block 网格对齐到 code-block group 起点），
// 因为输入语料由同一编码器家族产出，包顺序必须逐比特一致。

package pack

import (
	"fmt"
	"math"
)

// jpcSeg 是一个 code block 的一个编码段：一段连续的编码通道（coding pass）
// 以及承载它们的 body 字节。
type jpcSeg struct {
	passno    int
	numpasses int
	maxpasses int
	cnt       int
	bytes     []byte
}

// jpcCblk 表示一个 code block 及其解码状态。
type jpcCblk struct {
	x0, y0, x1, y1 int // 在所属 band 坐标系下的边界

	numpasses   int
	firstpassno int
	numimsbs    int
	numlenbits  int
	segs        []*jpcSeg
	curseg      int // 第一个未完成段的索引；没有时等于 len(segs)
}

// jpcPrc 是一个 band 的一个 precinct：该分辨率级的 precinct 网格与此 band 的
// 交集、其中的 code-block 网格，以及包头中描述这些 code block 的两棵 tag tree。
// 与 band 不相交的 precinct 既没有 code block，也没有 tag tree。
type jpcPrc struct {
	xstart, ystart, xend, yend int // 分辨率级坐标
	cblks                      []*jpcCblk
	inclTree, numimsbTree      *jpcTagTree
}

// jpcBand 是一个分辨率级的一个子带（subband）。
type jpcBand struct {
	xstart, ystart, xend, yend int // 分辨率级坐标
	locX, locY                 int // (xstart, ystart) 在合成数组中的位置
	orient                     int
	numbps                     int
	prcs                       []*jpcPrc // 每个 precinct 一项
}

// jpcLevel 是一个分辨率级（索引 0 = 最粗）。
type jpcLevel struct {
	xstart, ystart, xend, yend int
	numprcs                    int
	bands                      []*jpcBand
}

// jpcTileDecoder 保存唯一受支持 tile 的几何信息。
type jpcTileDecoder struct {
	img      *jpcImage
	tx0, ty0 int
	w, h     int
	data     []int32
	levels   []*jpcLevel
	pos      int // tile 数据中的读取位置
}

func ceilDivPow2(x, n int) int  { return (x + (1 << n) - 1) >> n }
func floorDivPow2(x, n int) int { return x >> n }

// buildGeometry 为唯一的分量构建分辨率级、band、precinct 与 code block。
func (t *jpcTileDecoder) buildGeometry() error {
	cs := &t.img.cs
	numrlvls := cs.numdlvls + 1
	if numrlvls > 32 {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("%d resolution levels", numrlvls)}
	}
	numbandsTotal := 3*(numrlvls-1) + 1
	if len(t.img.quant.expns) < numbandsTotal {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("QCD provides %d step sizes, the codestream needs %d", len(t.img.quant.expns), numbandsTotal)}
	}

	// band 边界，顺序与参考变换表产出的顺序一致：
	// 先是 LL（最粗），随后每级依次是 HL、LH、HH。
	bands := jpcTsfbBands(t.tx0, t.ty0, t.tx0+t.w, t.ty0+t.h, cs.numdlvls)
	if len(bands) != numbandsTotal {
		return &ErrJPCMalformed{Reason: fmt.Sprintf("computed %d bands, expected %d", len(bands), numbandsTotal)}
	}

	t.levels = make([]*jpcLevel, numrlvls)
	for rlvlno := 0; rlvlno < numrlvls; rlvlno++ {
		shift := numrlvls - 1 - rlvlno
		lvl := &jpcLevel{
			xstart: ceilDivPow2(t.tx0, shift),
			ystart: ceilDivPow2(t.ty0, shift),
			xend:   ceilDivPow2(t.tx0+t.w, shift),
			yend:   ceilDivPow2(t.ty0+t.h, shift),
		}
		if lvl.xstart >= lvl.xend || lvl.ystart >= lvl.yend {
			t.levels[rlvlno] = lvl
			continue
		}

		// precinct 指数：设置 precinct 标志时码流会逐级声明；否则每个 band 只含
		// 单个 precinct，参考实现用指数 15 来表达这一情形。
		prcWexpn, prcHexpn := 15, 15
		if cs.csty&jpcCoxPrt != 0 {
			prcWexpn, prcHexpn = cs.prcWexpn[rlvlno], cs.prcHexpn[rlvlno]
		}
		tlprcxstart := floorDivPow2(lvl.xstart, prcWexpn) << prcWexpn
		tlprcystart := floorDivPow2(lvl.ystart, prcHexpn) << prcHexpn
		brprcxend := ceilDivPow2(lvl.xend, prcWexpn) << prcWexpn
		brprcyend := ceilDivPow2(lvl.yend, prcHexpn) << prcHexpn
		numhprcs := (brprcxend - tlprcxstart) >> prcWexpn
		numvprcs := (brprcyend - tlprcystart) >> prcHexpn
		lvl.numprcs = numhprcs * numvprcs
		if lvl.numprcs < 1 || lvl.numprcs >= 1<<16 {
			return &ErrJPCMalformed{Reason: fmt.Sprintf("level %d has %dx%d precincts", rlvlno, numhprcs, numvprcs)}
		}
		var tlcbgxstart, tlcbgystart, brcbgxend int
		var cbgWexpn, cbgHexpn int
		if rlvlno == 0 {
			tlcbgxstart, tlcbgystart = tlprcxstart, tlprcystart
			brcbgxend = brprcxend
			cbgWexpn, cbgHexpn = prcWexpn, prcHexpn
		} else {
			tlcbgxstart = ceilDivPow2(tlprcxstart, 1)
			tlcbgystart = ceilDivPow2(tlprcystart, 1)
			brcbgxend = ceilDivPow2(brprcxend, 1)
			if prcWexpn < 1 || prcHexpn < 1 {
				return &ErrJPCMalformed{Reason: fmt.Sprintf("level %d precinct exponents %d/%d make the code-block group exponent negative", rlvlno, prcWexpn, prcHexpn)}
			}
			cbgWexpn, cbgHexpn = prcWexpn-1, prcHexpn-1
		}
		cblkWexpn := cs.cblkWexpn
		if cbgWexpn < cblkWexpn {
			cblkWexpn = cbgWexpn
		}
		cblkHexpn := cs.cblkHexpn
		if cbgHexpn < cblkHexpn {
			cblkHexpn = cbgHexpn
		}

		numbands := 1
		if rlvlno > 0 {
			numbands = 3
		}
		lvl.bands = make([]*jpcBand, numbands)
		for bandno := 0; bandno < numbands; bandno++ {
			bndno := 0
			if rlvlno > 0 {
				bndno = 3*(rlvlno-1) + bandno + 1
			}
			bnd := bands[bndno]
			band := &jpcBand{
				xstart: bnd.xstart,
				ystart: bnd.ystart,
				xend:   bnd.xend,
				yend:   bnd.yend,
				locX:   bnd.locX - t.tx0,
				locY:   bnd.locY - t.ty0,
				orient: bnd.orient,
				numbps: t.img.quant.numguard + t.img.quant.expns[bndno] - 1,
			}
			if band.numbps < 0 {
				return &ErrJPCMalformed{Reason: fmt.Sprintf("band %d has %d bit planes", bndno, band.numbps)}
			}
			if band.xstart >= band.xend || band.ystart >= band.yend {
				lvl.bands[bandno] = band // 空 band：包解码时跳过
				continue
			}

			band.prcs = make([]*jpcPrc, lvl.numprcs)
			cbgxstart, cbgystart := tlcbgxstart, tlcbgystart
			for prcno := 0; prcno < lvl.numprcs; prcno++ {
				cbgxend := cbgxstart + (1 << cbgWexpn)
				cbgyend := cbgystart + (1 << cbgHexpn)
				prc := &jpcPrc{}
				band.prcs[prcno] = prc
				prc.xstart = cbgxstart
				if band.xstart > prc.xstart {
					prc.xstart = band.xstart
				}
				prc.ystart = cbgystart
				if band.ystart > prc.ystart {
					prc.ystart = band.ystart
				}
				prc.xend = cbgxend
				if band.xend < prc.xend {
					prc.xend = band.xend
				}
				prc.yend = cbgyend
				if band.yend < prc.yend {
					prc.yend = band.yend
				}
				if prc.xend > prc.xstart && prc.yend > prc.ystart {
					tlcblkxstart := floorDivPow2(prc.xstart, cblkWexpn) << cblkWexpn
					tlcblkystart := floorDivPow2(prc.ystart, cblkHexpn) << cblkHexpn
					brcblkxend := ceilDivPow2(prc.xend, cblkWexpn) << cblkWexpn
					brcblkyend := ceilDivPow2(prc.yend, cblkHexpn) << cblkHexpn
					numhcblks := (brcblkxend - tlcblkxstart) >> cblkWexpn
					numvcblks := (brcblkyend - tlcblkystart) >> cblkHexpn
					if numhcblks < 1 || numvcblks < 1 {
						return &ErrJPCMalformed{Reason: fmt.Sprintf("level %d band %d precinct %d: empty code-block grid", rlvlno, bandno, prcno)}
					}
					if numhcblks*numvcblks > 1<<20 {
						return &ErrJPCMalformed{Reason: fmt.Sprintf("level %d band %d precinct %d: %dx%d code blocks", rlvlno, bandno, prcno, numhcblks, numvcblks)}
					}
					prc.inclTree = newTagTree(numhcblks, numvcblks)
					prc.numimsbTree = newTagTree(numhcblks, numvcblks)

					cw := 1 << cblkWexpn
					ch := 1 << cblkHexpn
					cblkxstart, cblkystart := cbgxstart, cbgystart
					for {
						tmpxstart := cblkxstart
						if prc.xstart > tmpxstart {
							tmpxstart = prc.xstart
						}
						tmpystart := cblkystart
						if prc.ystart > tmpystart {
							tmpystart = prc.ystart
						}
						tmpxend := cblkxstart + cw
						if prc.xend < tmpxend {
							tmpxend = prc.xend
						}
						tmpyend := cblkystart + ch
						if prc.yend < tmpyend {
							tmpyend = prc.yend
						}
						if tmpxend > tmpxstart && tmpyend > tmpystart {
							prc.cblks = append(prc.cblks, &jpcCblk{
								x0:          tmpxstart - bnd.xstart,
								y0:          tmpystart - bnd.ystart,
								x1:          tmpxend - bnd.xstart,
								y1:          tmpyend - bnd.ystart,
								numlenbits:  3,
								curseg:      0,
								firstpassno: -1,
							})
						}
						cblkxstart += cw
						if cblkxstart >= cbgxend {
							cblkxstart = cbgxstart
							cblkystart += ch
							if cblkystart >= cbgyend {
								break
							}
						}
					}
					if len(prc.cblks) != numhcblks*numvcblks {
						return &ErrJPCMalformed{Reason: fmt.Sprintf("level %d band %d precinct %d: enumerated %d code blocks, geometry predicts %d", rlvlno, bandno, prcno, len(prc.cblks), numhcblks*numvcblks)}
					}
				}
				cbgxstart += 1 << cbgWexpn
				if cbgxstart >= brcbgxend {
					cbgxstart = tlcbgxstart
					cbgystart += 1 << cbgHexpn
				}
			}
			lvl.bands[bandno] = band
		}
		t.levels[rlvlno] = lvl
	}
	return nil
}

// jpcTsfbBand 是变换表中的一个子带。
type jpcTsfbBand struct {
	xstart, ystart, xend, yend int
	locX, locY                 int
	orient                     int
}

// jpcTsfbBands 复刻参考实现的子带边界计算：递归先产出 LL，再从最粗到最细，
// 对每一级依次产出 HL、LH、HH。
func jpcTsfbBands(xstart, ystart, xend, yend int, numlvls int) []jpcTsfbBand {
	out := make([]jpcTsfbBand, 0, 3*numlvls+1)
	var rec func(locX, locY, x0, y0, x1, y1, n int)
	rec = func(locX, locY, x0, y0, x1, y1, n int) {
		if n > 0 {
			nx0 := ceilDivPow2(x0, 1)
			ny0 := ceilDivPow2(y0, 1)
			nx1 := ceilDivPow2(x1, 1)
			ny1 := ceilDivPow2(y1, 1)
			rec(locX, locY, nx0, ny0, nx1, ny1, n-1)

			out = append(out, jpcTsfbBand{
				xstart: floorDivPow2(x0, 1),
				ystart: ny0,
				xend:   floorDivPow2(x1, 1),
				yend:   ny1,
				locX:   locX + nx1 - nx0,
				locY:   locY,
				orient: jpcOrientHL,
			})
			out = append(out, jpcTsfbBand{
				xstart: nx0,
				ystart: floorDivPow2(y0, 1),
				xend:   nx1,
				yend:   floorDivPow2(y1, 1),
				locX:   locX,
				locY:   locY + ny1 - ny0,
				orient: jpcOrientLH,
			})
			out = append(out, jpcTsfbBand{
				xstart: floorDivPow2(x0, 1),
				ystart: floorDivPow2(y0, 1),
				xend:   floorDivPow2(x1, 1),
				yend:   floorDivPow2(y1, 1),
				locX:   locX + nx1 - nx0,
				locY:   locY + ny1 - ny0,
				orient: jpcOrientHH,
			})
			return
		}
		out = append(out, jpcTsfbBand{
			xstart: x0,
			ystart: y0,
			xend:   x1,
			yend:   y1,
			locX:   locX,
			locY:   locY,
			orient: jpcOrientLL,
		})
	}
	if numlvls > 0 {
		rec(xstart, ystart, xstart, ystart, xend, yend, numlvls)
	} else {
		rec(xstart, ystart, xstart, ystart, xend, yend, 0)
	}
	return out
}

// segPassCnt 是普通模式（无 bypass、无终止）下参考实现的段通道数。

func segPassCnt(passno, firstpassno int) int {
	_ = firstpassno
	const jpcPrec = 32
	ret := jpcPrec*3 - 2
	const numpasses = 10000
	if ret > numpasses-passno {
		ret = numpasses - passno
	}
	return ret
}

// decodePackets 按 LRCP 顺序遍历（先 layer，再按最粗在前的分辨率级，然后是唯一
// 的分量，最后按网格顺序遍历 precinct），依次解码每个包头及其 body 字节。
func (t *jpcTileDecoder) decodePackets() error {
	for rlvlno, lvl := range t.levels {
		if lvl.bands == nil {
			continue // 空分辨率级：遍历不会产出包
		}
		for prcno := 0; prcno < lvl.numprcs; prcno++ {
			if t.pos >= len(t.img.tileData) {
				return &ErrJPCMalformed{Reason: fmt.Sprintf("tile data exhausted at resolution level %d precinct %d", rlvlno, prcno)}
			}
			br := newJPCBitReader(t.img.tileData, t.pos)
			if err := t.decodePacket(br, lvl, 0, prcno); err != nil {
				return err
			}
		}
	}
	return nil
}

// decodePacket 解码一个包（一个分辨率级、一个 precinct），随后消费其各段声明
// 的 body 字节。
func (t *jpcTileDecoder) decodePacket(br *jpcBitReader, lvl *jpcLevel, lyrno, prcno int) error {
	present := br.getbit()
	if present < 0 {
		return &ErrJPCMalformed{Reason: "packet header truncated"}
	}
	if present == 0 {
		br.align()
		t.pos = br.bytePos()
		return nil
	}
	for _, band := range lvl.bands {
		if band == nil || prcno >= len(band.prcs) {
			continue
		}
		prc := band.prcs[prcno]
		if prc == nil || len(prc.cblks) == 0 {
			continue
		}
		for cblkno, cb := range prc.cblks {
			var included bool
			if cb.numpasses == 0 {
				inc, err := prc.inclTree.decode(cblkno, lyrno+1, br)
				if err != nil {
					return err
				}
				// 当叶子节点的值低于该 layer 阈值时 tag tree 返回 1，
				// 这正是“包含在此包中”的含义。
				included = inc == 1
			} else {
				b := br.getbit()
				if b < 0 {
					return &ErrJPCMalformed{Reason: "code-block inclusion bit truncated"}
				}
				included = b == 1
			}
			if !included {
				continue
			}
			if cb.numpasses == 0 {
				i := 1
				for {
					ret, err := prc.numimsbTree.decode(cblkno, i, br)
					if err != nil {
						return err
					}
					if ret != 0 {
						break
					}
					i++
					if i > 10000 {
						return &ErrJPCMalformed{Reason: "unreasonable number of insignificant bit planes"}
					}
				}
				cb.numimsbs = i - 1
				cb.firstpassno = cb.numimsbs * 3
			}
			numnewpasses := jpcGetNumNewPasses(br)
			if numnewpasses < 0 {
				// 截断二进制码读过了包头末尾；jpcGetNumNewPasses 会把 getbit 的
				// -1（EOF）信号向上传递（bug.md §2.9）。
				return &ErrJPCMalformed{Reason: "number of new coding passes ran past the end of the packet header"}
			}
			savenumnewpasses := numnewpasses
			if numnewpasses > 0 {
				if cb.firstpassno > 10000 {
					return &ErrJPCMalformed{Reason: "first pass number unreasonably large"}
				}
				m, err := jpcGetCommaCode(br)
				if err != nil {
					return err
				}
				cb.numlenbits += m
				seg := cb.curseg
				for numnewpasses > 0 {
					passno := cb.firstpassno + cb.numpasses + (savenumnewpasses - numnewpasses)
					if passno >= 10000 {
						return &ErrJPCMalformed{Reason: "pass number unreasonably large"}
					}
					maxpasses := segPassCnt(passno, cb.firstpassno)
					if seg >= len(cb.segs) {
						cb.segs = append(cb.segs, &jpcSeg{passno: passno, maxpasses: maxpasses})
						seg = len(cb.segs) - 1
					}
					s := cb.segs[seg]
					n := numnewpasses
					if maxpasses < n {
						n = maxpasses
					}
					numnewpasses -= n
					length := br.getbits(cb.numlenbits + jpcFloorLog2(n))
					if length < 0 {
						return &ErrJPCMalformed{Reason: "code-block length code truncated"}
					}
					s.numpasses += n
					s.cnt = length
					seg++
				}
			}
			cb.numpasses += savenumnewpasses
		}
	}
	br.align()

	// 包 body：code-block 的段字节紧随包头，按 band、precinct、code-block、
	// 段的顺序排列。
	pos := br.bytePos()
	for _, band := range lvl.bands {
		if band == nil || prcno >= len(band.prcs) {
			continue
		}
		prc := band.prcs[prcno]
		if prc == nil || len(prc.cblks) == 0 {
			continue
		}
		for _, cb := range prc.cblks {
			for i := cb.curseg; i < len(cb.segs); i++ {
				s := cb.segs[i]
				if s.cnt > 0 {
					if pos+s.cnt > len(t.img.tileData) {
						return &ErrJPCMalformed{Reason: fmt.Sprintf("code-block segment needs %d bytes, only %d remain", s.cnt, len(t.img.tileData)-pos)}
					}
					s.bytes = append(s.bytes, t.img.tileData[pos:pos+s.cnt]...)
					pos += s.cnt
					s.cnt = 0
				}
				if s.numpasses >= s.maxpasses {
					cb.curseg = i + 1
				}
			}
		}
	}
	t.pos = pos
	return nil
}

// decodeCodeBlocks 对每个获得了编码通道的 code block 运行 tier-1 解码器。
func (t *jpcTileDecoder) decodeCodeBlocks() error {
	for _, lvl := range t.levels {
		for _, band := range lvl.bands {
			if band == nil {
				continue
			}
			for _, prc := range band.prcs {
				if prc == nil || len(prc.cblks) == 0 {
					continue
				}
				if err := t.decodePrecinctBlocks(band, prc); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// decodePrecinctBlocks 对一个 band 的一个 precinct 中的 code block 运行 tier-1
// 解码器。
func (t *jpcTileDecoder) decodePrecinctBlocks(band *jpcBand, prc *jpcPrc) error {
	for _, cb := range prc.cblks {
		if len(cb.segs) == 0 {
			continue
		}
		cw, ch := cb.x1-cb.x0, cb.y1-cb.y0
		dbase := (band.locY+cb.y0)*t.w + band.locX + cb.x0
		p := newCblkPass(cw, ch, t.data, dbase, t.w)
		mq := newMQDecoder(nil)
		mq.resetContexts()
	segs:
		for _, seg := range cb.segs {
			mq.data = seg.bytes
			mq.pos = 0
			mq.init()
			for i := 0; i < seg.numpasses; i++ {
				passno := seg.passno + i
				if passno >= 10000 {
					return &ErrJPCMalformed{Reason: "pass number unreasonably large"}
				}
				bpno := band.numbps - 1 - (cb.numimsbs + (passno-cb.firstpassno+2)/3)
				if bpno < 0 {
					// 参考解码器在此处停止对该 code block 的编码而非报错：
					// 码流请求的位平面数超过了该 band 实际拥有的数量。
					break segs
				}
				if bpno > 30 {
					return &ErrJPCMalformed{Reason: fmt.Sprintf("bit plane index %d too large", bpno)}
				}
				switch passno % 3 {
				case 0:
					p.clnpass(mq, bpno, band.orient)
				case 1:
					p.sigpass(mq, bpno, band.orient)
				default:
					p.refpass(mq, bpno)
				}
			}
		}
	}
	return nil
}

// synthesize 施加 5/3 逆变换，从最粗的分辨率级开始。
func (t *jpcTileDecoder) synthesize() {
	numlvls := t.img.cs.numdlvls
	type reg struct{ xstart, ystart, w, h int }
	regs := make([]reg, 0, numlvls+1)
	regs = append(regs, reg{t.tx0, t.ty0, t.w, t.h})
	for i := 0; i < numlvls; i++ {
		p := regs[len(regs)-1]
		xs := ceilDivPow2(p.xstart, 1)
		ys := ceilDivPow2(p.ystart, 1)
		regs = append(regs, reg{xs, ys, ceilDivPow2(p.xstart+p.w, 1) - xs, ceilDivPow2(p.ystart+p.h, 1) - ys})
	}
	// jpcJoin1D 会在 tmp 中暂存 llen = (n+bool(parity==0))>>1 个元素。行方向
	// 处理拼接 n = r.w 个值（llen <= t.w），但列方向处理拼接 n = r.h 个值，
	// 因此对最宽的区域而言 tmp 必须容纳 (r.h+1)/2 >= (t.h+1)/2（regs[0] 的
	// r.h 最大，等于 t.h）。只按 t.w 分配 tmp 会在 w=1、h=100000 这类高而窄的
	// tile 上溢出（bug.md §1.7）。
	tmpLen := t.w
	if hh := (t.h + 1) / 2; hh > tmpLen {
		tmpLen = hh
	}
	tmp := make([]int32, tmpLen)
	// 最粗的区域只含最低频的低通 band，不参与提升；每个提升步骤扩展出一个更细
	// 的级，因此恰好有 numdlvls 个提升步骤。
	for i := len(regs) - 2; i >= 0; i-- {
		r := regs[i]
		if r.w == 0 || r.h == 0 {
			continue
		}
		colparity := r.xstart & 1
		rowparity := r.ystart & 1
		for row := 0; row < r.h; row++ {
			base := row * t.w
			jpcInvLift53(t.data, base, r.w, 1, colparity)
			jpcJoin1D(t.data, base, r.w, 1, colparity, tmp)
		}
		for col := 0; col < r.w; col++ {
			jpcInvLift53(t.data, col, r.h, t.w, rowparity)
			jpcJoin1D(t.data, col, r.h, t.w, rowparity, tmp)
		}
	}
}

// jpcInvLift53 对一条按 [低通..., 高通...] 布局的跨步（strided）线施加一次
// 5/3 逆提升步骤。
func jpcInvLift53(a []int32, base, n, step, parity int) {
	llen := (n + boolInt(parity == 0)) >> 1
	endParity := parity == (n & 1)
	if n > 1 {
		li, hi := 0, llen
		if parity == 0 {
			a[base] -= (a[base+hi*step] + 1) >> 1
			li++
		}
		for cnt := llen - boolInt(parity == 0) - boolInt(!endParity); cnt > 0; cnt-- {
			a[base+li*step] -= (a[base+hi*step] + a[base+(hi+1)*step] + 2) >> 2
			li++
			hi++
		}
		if !endParity {
			a[base+li*step] -= (a[base+hi*step] + 1) >> 1
		}

		li, hi = 0, llen
		if parity == 1 {
			a[base+hi*step] += a[base]
			hi++
		}
		for cnt := n - llen - parity - boolInt(endParity); cnt > 0; cnt-- {
			a[base+hi*step] += (a[base+li*step] + a[base+(li+1)*step]) >> 1
			hi++
			li++
		}
		if endParity {
			a[base+hi*step] += a[base+li*step]
		}
		return
	}
	if parity == 1 {
		a[base] >>= 1
	}
}

// jpcJoin1D 把一条线的低通与高通通道交错还原回各自原来的位置。
func jpcJoin1D(a []int32, base, n, step, parity int, tmp []int32) {
	llen := (n + boolInt(parity == 0)) >> 1
	for i := 0; i < llen; i++ {
		tmp[i] = a[base+i*step]
	}
	for i := 0; i < n-llen; i++ {
		a[base+(1-parity+2*i)*step] = a[base+(llen+i)*step]
	}
	for i := 0; i < llen; i++ {
		a[base+(parity+2*i)*step] = tmp[i]
	}
}

// 单幅解码图像（每样本一个 int32）的资源上限。单个维度可以合法地很大：CMA 会
// 把某些格点场写成单行码流（例如 494841x1），因此单维上限与总样本数上限是分开
// 的。jpcMaxSamples 限制实际分配的大小，同时也约束常量场路径（bitsPerValue = 0），
// 后者会直接分配 NumValues 个 int32（bug.md §1.6）。
const (
	jpcMaxDimension = 1 << 21
	jpcMaxSamples   = 1 << 24
)

// jpcDecodeSamples 将 JPEG 2000 码流解码为按光栅顺序排列的分量样本，并施加
// 无符号电平偏移（level shift）。
func jpcDecodeSamples(data []byte) (samples []int32, w, h, prec int, err error) {
	img, err := parseJPC(data)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	// 唯一受支持的布局是覆盖整幅图像的单个 tile。
	numTilesX := (img.xsiz - img.xt0siz + img.xtsiz - 1) / img.xtsiz
	numTilesY := (img.ysiz - img.yt0siz + img.ytsiz - 1) / img.ytsiz
	if numTilesX != 1 || numTilesY != 1 {
		return nil, 0, 0, 0, &ErrJPCUnsupported{Feature: "multi-tile image", Detail: fmt.Sprintf("%dx%d tiles", numTilesX, numTilesY)}
	}
	tx0 := img.xt0siz
	if img.xosiz > tx0 {
		tx0 = img.xosiz
	}
	tx1 := img.xt0siz + img.xtsiz
	if img.xsiz < tx1 {
		tx1 = img.xsiz
	}
	ty0 := img.yt0siz
	if img.yosiz > ty0 {
		ty0 = img.yosiz
	}
	ty1 := img.yt0siz + img.ytsiz
	if img.ysiz < ty1 {
		ty1 = img.ysiz
	}
	if tx1 <= tx0 || ty1 <= ty0 {
		return nil, 0, 0, 0, &ErrJPCMalformed{Reason: "tile does not intersect the image area"}
	}
	w = tx1 - tx0
	h = ty1 - ty0
	// 针对实际分配的资源的守卫（每样本一个 int32）。单个维度可以合法地很大：
	// CMA 会把某些格点场写成单行码流（例如 494841x1），因此既要限制每个维度，
	// 也要限制样本总数。
	if w > jpcMaxDimension || h > jpcMaxDimension || w*h > jpcMaxSamples {
		return nil, 0, 0, 0, &ErrJPCMalformed{Reason: fmt.Sprintf("image %dx%d too large", w, h)}
	}

	t := &jpcTileDecoder{img: img, tx0: tx0, ty0: ty0, w: w, h: h, data: make([]int32, w*h)}
	if err := t.buildGeometry(); err != nil {
		return nil, 0, 0, 0, err
	}
	if err := t.decodePackets(); err != nil {
		return nil, 0, 0, 0, err
	}
	if err := t.decodeCodeBlocks(); err != nil {
		return nil, 0, 0, 0, err
	}
	t.synthesize()

	prec = img.comps[0].prec
	if img.comps[0].sgnd == 0 {
		shift := int32(1) << (prec - 1)
		for i := range t.data {
			t.data[i] += shift
		}
	}
	return t.data, w, h, prec, nil
}

// jpcUnpacker 解码 GRIB2 数据表示模板 5.40（JPEG 2000 code stream 格式），
// 对应本文件开头所述的无损 5/3 子集。
type jpcUnpacker struct{}

func (jpcUnpacker) Name() string { return "JPEG 2000 code stream (5.40), 5/3 lossless subset" }

func (jpcUnpacker) Unpack(ctx *Context) ([]float32, error) {
	if ctx.Fields == nil || ctx.Fields.Len() < 7 {
		return nil, fmt.Errorf("pack: template 5.40: section 5 fields missing or truncated (walk len %d)", walkLen(ctx.Fields))
	}
	R := ctx.Fields.At(0).F
	E := int16(smInt16(uint16(ctx.Fields.At(1).U)))
	D := int16(smInt16(uint16(ctx.Fields.At(2).U)))
	compressionType := uint8(ctx.Fields.At(5).U)
	if compressionType != 0 {
		return nil, &ErrJPCUnsupported{
			Feature: "lossy JPEG 2000 packing",
			Detail:  fmt.Sprintf("octet 22 = %d (only 'Lossless' is implemented)", compressionType),
		}
	}

	// bitsPerValue = 0 声明根本没有打包任何样本，因此每个值都等于参考值
	// （样本索引为零）。非空的数据段与该声明矛盾，需报错而非忽略。
	// 此路径会直接分配 NumValues 个 int32，绕过了 jpcDecodeSamples 的 w*h
	// 守卫，所以在这里对声明的数量做上限约束（bug.md §1.6）。
	bits := uint8(ctx.Fields.At(3).U)
	if bits == 0 {
		if len(ctx.Data) != 0 {
			return nil, &ErrJPCMalformed{
				Reason: fmt.Sprintf("bitsPerValue = 0 with a %d-byte data section", len(ctx.Data)),
			}
		}
		if ctx.NumValues < 0 || ctx.NumValues > jpcMaxSamples {
			return nil, &ErrJPCMalformed{
				Reason: fmt.Sprintf("bitsPerValue = 0 declares numberOfValues = %d, above the %d-sample resource limit", ctx.NumValues, jpcMaxSamples),
			}
		}
		return jpcScaleValues(make([]int32, ctx.NumValues), E, D, R), nil
	}

	samples, w, h, prec, err := jpcDecodeSamples(ctx.Data)
	if err != nil {
		return nil, err
	}
	// 码流覆盖整个网格；配合 bitmap 时它可能包含全部格点，也可能只包含有效
	// 格点。只有在样本数完全吻合时才接受这两种读法，而当 bitmap 把每个点都标记
	// 为有效时二者是一致的。
	var picked []int32
	switch {
	case w*h == ctx.NumValues:
		picked = samples
	case w*h == ctx.GridPoints && ctx.Bitmap != nil:
		picked = make([]int32, 0, ctx.NumValues)
		for i := 0; i < ctx.GridPoints; i++ {
			if ctx.Bitmap.Bit(i) {
				picked = append(picked, samples[i])
			}
		}
		if len(picked) != ctx.NumValues {
			return nil, fmt.Errorf("pack: template 5.40: bitmap selects %d points, expected %d", len(picked), ctx.NumValues)
		}
	default:
		return nil, &ErrJPCMalformed{
			Reason: fmt.Sprintf("codestream (%d-bit component) holds %dx%d = %d samples, the grid expects %d (or %d with the bitmap)", prec, w, h, w*h, ctx.GridPoints, ctx.NumValues),
		}
	}
	return jpcScaleValues(picked, E, D, R), nil
}

// jpcScaleValues 使用第 5 节的参考值 R、指数 E 与除数 D 将解码出的分量样本映射
// 为 GRIB2 值。运算顺序与 simple-packing 解码器一致，从而两个模板对相同字段
// 产出完全相同的结果。
func jpcScaleValues(picked []int32, E, D int16, R float32) []float32 {
	twoE := math.Pow(2, float64(E))
	fR := float64(R)
	tenMinusD := math.Pow(10, -float64(D))
	eZero, dZero := E == 0, D == 0
	out := make([]float32, len(picked))
	for i, raw := range picked {
		if raw >= 0 {
			out[i] = scaleSimple(uint32(raw), twoE, fR, tenMinusD, eZero, dZero)
			continue
		}
		// 有符号分量可能产生负样本；此处沿用相同的公式与运算顺序。
		switch {
		case eZero && dZero:
			out[i] = float32(float64(raw) + fR)
		case eZero:
			out[i] = float32((float64(raw) + fR) * tenMinusD)
		case dZero:
			out[i] = float32(float64(raw)*twoE + fR)
		default:
			out[i] = float32((float64(raw)*twoE + fR) * tenMinusD)
		}
	}
	return out
}
