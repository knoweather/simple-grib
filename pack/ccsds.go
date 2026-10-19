//
// CCSDS recommended lossless compression (data representation template
// 5.42) decode core — a pure-Go port of the libaec adaptive entropy coder
// (CCSDS 121.0-B-3), modelled on libaec 1.1.3 src/decode.c and its FSM
// arithmetic decoder with the m_id / m_low_entropy / m_split / m_zero_block
// / m_se / m_uncomp modes.
//
// Mirrors the eccodes pipeline (DataCcsdsPacking::unpack on master):
// sec7 data (from octet 6) = the AEC bitstream -> pass
// {flags=ccsdsFlags, bits_per_sample=bpv, block_size, rsi} to
// aec_buffer_decode -> NV fixed-width samples -> val = (sample*2^B + R) * 10^(-D).
//
// The "modify flags" step eccodes performs before calling the codec
// (modify_aec_flags: clear AEC_DATA_3BYTE, force MSB to host byte order)
// only affects the byte serialization of samples — never the bitstream.
// This port emits uint32 sample values straight from the RSI buffer, so no
// byte-order or 3-byte path exists at all; only the SIGNED and PREPROCESS
// flags change the decoded values.
//
// Golden calibration (against eccodes 2.41 ground truth recorded on the Linux bench host):
// aifs025_oper.grib2 (w) and ifs025_oper.grib2 (slor), both with
// bpv=16 flags=14 blockSize=32 rsi=128, NV=1,038,240 — value-array summary
// anchors are in scripts/xcheck/ccsds-oracle.sh.

package pack

import (
	"fmt"
	"math"
	"math/bits"
)

// CCSDS flag bits (libaec aec.h / eccodes template.5.42.def flag order):
// bit0 SIGNED, bit1 3BYTE, bit2 MSB, bit3 PREPROCESS, bit4 RESTRICTED,
// bit5 PAD_RSI. Only SIGNED and PREPROCESS change decoded values; the rest
// are serialization/restriction options (see the package comment).
const (
	ccsdsFlagSigned     = 1 << 0
	ccsdsFlag3Byte      = 1 << 1
	ccsdsFlagMSB        = 1 << 2
	ccsdsFlagPreprocess = 1 << 3
	ccsdsFlagRestricted = 1 << 4
	ccsdsFlagPadRSI     = 1 << 5
)

// ccsdROS 是 libaec 的 ROS 游程码，编码「连续许多个零块」的重复结构。
// ccsdROS is libaec's ROS run-length code: it encodes a run of many zero blocks.
const ccsdROS = 5

// Upper bounds for template 5.42 scale factors. B and D are sign-magnitude
// int16 (up to ±32767), but 2^B and 10^D overflow float64 long before that:
// 2^1024 = +Inf and 10^309 = +Inf, so an absurd exponent would poison every
// decoded value with Inf (bug.md §2.11). Values |B| > 1023 and |D| > 308 are
// therefore rejected — the largest exponents for which 2^B and 10^D remain
// finite (the mirror case 10^-D only underflows to 0 at D=323, so the ±308
// bound also keeps both scaling directions from degenerating). eccodes does
// no such check; this is a deliberate fail-loud deviation.
const (
	ccsdsMaxBinaryExp  = 1023
	ccsdsMaxDecimalExp = 308
)

// ccsdsScaleFactors validates the binary/decimal scale factors and returns
// the multipliers 2^B and 10^-D used by the template 5.42 formula
// (sample*2^B + R)*10^-D.
func ccsdsScaleFactors(B, D int16) (twoB, tenMinusD float64, err error) {
	if B < -ccsdsMaxBinaryExp || B > ccsdsMaxBinaryExp {
		return 0, 0, &ErrUnsupportedValue{Template: TemplateCCSDS, Field: "binary scale factor", Value: int64(B)}
	}
	if D < -ccsdsMaxDecimalExp || D > ccsdsMaxDecimalExp {
		return 0, 0, &ErrUnsupportedValue{Template: TemplateCCSDS, Field: "decimal scale factor", Value: int64(D)}
	}
	return math.Pow(2, float64(B)), math.Pow(10, -float64(D)), nil
}

// ccsdsParams carries the template 5.42 header fields that drive the codec.
type ccsdsParams struct {
	Flags      uint8 // ccsdsFlags byte (section 5)
	BitsPerVal int   // bits per sample (1..32; 0 = constant field, rejected)
	BlockSize  int   // samples per entropy block (ccsdsBlockSize)
	RSI        int   // blocks per reference sample interval (ccsdsRsi)
}

// ccsdsDecode runs the full AEC bitstream and returns the decoded sample
// values (pre-scaling; the caller applies (s*2^B+R)*10^-D). wantSamples is
// the expected count (numberOfValues): a legal stream yields exactly that
// many, and any other outcome is a loud failure.
func ccsdsDecode(p ccsdsParams, data []byte, wantSamples int) ([]uint32, error) {
	if p.BitsPerVal <= 0 || p.BitsPerVal > 32 {
		return nil, fmt.Errorf("pack: template 5.42: bitsPerValue = %d out of range [1,32]", p.BitsPerVal)
	}
	if p.BlockSize < 1 || p.RSI < 1 {
		return nil, fmt.Errorf("pack: template 5.42: blockSize = %d / rsi = %d invalid", p.BlockSize, p.RSI)
	}
	// numberOfValues is the decoder's output budget. A negative budget is
	// unrepresentable and would slice out of bounds; a zero budget together
	// with a data section means a corrupt stream (declared no values but
	// carries encoded data; the old code returned an empty slice + nil for
	// it, bug.md §2.10). A zero budget with no data is the legal empty field.
	if wantSamples < 0 {
		return nil, fmt.Errorf("pack: template 5.42: numberOfValues = %d is negative", wantSamples)
	}
	if wantSamples == 0 {
		if len(data) != 0 {
			return nil, fmt.Errorf("pack: template 5.42: numberOfValues = 0 with a %d-byte data section", len(data))
		}
		return []uint32{}, nil
	}

	d := &ccsdsDecoder{
		want:      wantSamples,
		params:    p,
		in:        data,
		signed:    p.Flags&ccsdsFlagSigned != 0,
		pp:        p.Flags&ccsdsFlagPreprocess != 0,
		padRSI:    p.Flags&ccsdsFlagPadRSI != 0,
		idLen:     idLenFor(p.BitsPerVal, p.Flags),
		xmax:      xmaxFor(p.BitsPerVal, p.Flags&ccsdsFlagSigned != 0),
		blockSize: p.BlockSize,
	}
	if d.pp {
		d.ref = 1
		d.encodedBlockSize = p.BlockSize - 1
	} else {
		d.ref = 0
		d.encodedBlockSize = p.BlockSize
	}
	d.inBlkLen = (p.BlockSize*p.BitsPerVal+d.idLen)/8 + 16

	modModes := 1 << d.idLen
	d.idTable = make([]ccsdsMode, modModes)
	d.idTable[0] = modeLowEntropy
	for i := 1; i < modModes-1; i++ {
		d.idTable[i] = modeSplit
	}
	d.idTable[modModes-1] = modeUncomp

	d.rsiBuf = make([]uint32, p.RSI*p.BlockSize)
	createSETable(&d.seTable)
	d.nextMode = modeID

	// Iterative FSM driver (libaec aec_decode): run the current mode until it
	// reports exit/error; mContinue hands control to that mode's successor.
	for {
		if d.want > 0 && len(d.out) >= d.want {
			// Output budget filled. This mirrors the reference implementation,
			// not a silent truncation: eccodes calls aec_buffer_decode with
			// avail_out = numberOfValues samples, and libaec stops once the
			// output buffer is full (libaec's avail_out semantics), leaving
			// the remaining stream bytes unconsumed. flush() never appends
			// past the budget, so here len(d.out) == wantSamples. Being
			// stricter (erroring when the stream carries more samples than
			// declared) would diverge from the eccodes baseline, so this
			// early stop is deliberate. Corruption the budget cannot express
			// — wantSamples == 0 with data — is rejected earlier.
			return d.out[:d.want], nil
		}
		switch d.dispatch() {
		case mContinue:
			continue // the mode set d.nextMode; the loop runs its successor
		case mExit:
			d.flush()
			if len(d.out) < wantSamples {
				return nil, fmt.Errorf("pack: template 5.42: decoded %d samples but numberOfValues = %d (stream truncated)", len(d.out), wantSamples)
			}
			return d.out[:wantSamples], nil
		default: // mError
			return nil, fmt.Errorf("pack: template 5.42: AEC bitstream corrupt")
		}
	}
}

func idLenFor(bpv int, flags uint8) int {
	switch {
	case bpv > 16:
		return 5
	case bpv > 8:
		return 4
	default:
		if flags&ccsdsFlagRestricted != 0 {
			switch {
			case bpv <= 2:
				return 1
			case bpv <= 4:
				return 2
			default:
				return 3
			}
		}
		return 3
	}
}

func xmaxFor(bpv int, signed bool) uint32 {
	if signed {
		return (1 << (bpv - 1)) - 1
	}
	return (1 << bpv) - 1
}

// FSM 状态码（libaec 的 M_CONTINUE / M_EXIT / M_ERROR）。
const (
	mContinue = iota
	mExit
	mError
)

// ccsdsMode 是状态机模式 id；分发器把它映射到对应的解码方法。
type ccsdsMode int

const (
	modeID ccsdsMode = iota
	modeLowEntropy
	modeSplit
	modeZeroBlock
	modeUncomp
	// slow (per-sample) paths, entered when a fast path's buffer check fails
	modeSplitFS
	modeSplitOutput
	modeSESlow
	modeUncompCopy
)

type ccsdsDecoder struct {
	params ccsdsParams

	// bit reader (mirrors libaec state: 64-bit accumulator, MSB first)
	in   []byte
	pos  int
	acc  uint64
	bitp int // valid bits in acc
	fs   int // fundamental-sequence zero counter (slow path)

	// stream configuration
	signed    bool
	pp        bool
	padRSI    bool
	idLen     int
	xmax      uint32
	blockSize int

	ref              int // 1 = the block carries one reference sample (when pp is on)
	encodedBlockSize int // samples encoded per block (blockSize-1 when pp is on)
	inBlkLen         int // worst-case input bytes for one block (fast-path checks)
	idTable          []ccsdsMode

	// per-mode working state
	id            uint32 // the last option id read by mID (libaec's state->id)
	sampleCounter int    // slow path: samples processed in the current block
	nextMode      ccsdsMode

	// RSI interval buffer + inverse-preprocessing state
	rsiBuf     []uint32
	rsiPos     int // write index into rsiBuf
	flushStart int // read index of the samples pending flush
	lastOut    uint32

	seTable [182]int32

	out  []uint32 // decoded samples, in order
	want int      // output budget (numberOfValues); 0 = decode to end of stream
}

// dispatch runs the mode selected by d.nextMode (set by mID / the
// transitions).
func (d *ccsdsDecoder) dispatch() int {
	switch d.nextMode {
	case modeID:
		return d.mID()
	case modeLowEntropy:
		return d.mLowEntropy()
	case modeSplit:
		return d.mSplit()
	case modeZeroBlock:
		return d.mZeroBlock()
	case modeUncomp:
		return d.mUncompCopy()
	case modeSplitFS:
		return d.mSplitFS()
	case modeSplitOutput:
		return d.mSplitOutput()
	case modeSESlow:
		return d.mSESlow()
	default:
		return mError
	}
}

// --- bit reader (libaec's direct_get / bits_ask / fs_ask / fs_drop) ---------

// ask ensures acc holds at least n valid bits, refilling from the input.
// Returns false once the input is exhausted (end-of-stream signal).
func (d *ccsdsDecoder) ask(n int) bool {
	for d.bitp < n {
		if d.pos >= len(d.in) {
			return false
		}
		d.acc = (d.acc << 8) | uint64(d.in[d.pos])
		d.pos++
		d.bitp += 8
	}
	return true
}

// getN mirrors libaec's direct_get: refill the accumulator from the input as
// needed, then return and discard the top n bits. Fast path only — the
// caller guarantees the input has enough bytes (inBlkLen / binaryPart
// checks). The clamping below is a defensive backstop ensuring a corrupt
// stream never indexes past the input (libaec master does the same:
// direct_get clamps byte loads to avail_in and zero-fills); a short stream is
// still converted into a loud failure by the final sample-count check
// (bug.md §5.3).
func (d *ccsdsDecoder) getN(n int) uint32 {
	for d.bitp < n {
		if d.pos >= len(d.in) {
			d.acc <<= 8 // zero-fill: never read out of bounds
			d.bitp += 8
			continue
		}
		d.acc = (d.acc << 8) | uint64(d.in[d.pos])
		d.pos++
		d.bitp += 8
	}
	d.bitp -= n
	return uint32(d.acc>>uint(d.bitp)) & ((1 << n) - 1)
}

// drop discards the top n valid bits without reading them.
func (d *ccsdsDecoder) drop(n int) { d.bitp -= n }

// getFSFast interprets one fundamental sequence in bulk (libaec's
// direct_get_fs): the count of consecutive zero bits up to the first 1 bit.
// Fast path only — a legal stream has the bits here; running dry mid-sequence
// leaves acc == 0 with bitp too low, and the sample-count check turns that
// into a loud failure.
func (d *ccsdsDecoder) getFSFast() uint32 {
	fs := uint32(0)
	if d.bitp > 0 {
		d.acc &= (1 << d.bitp) - 1
	} else {
		d.acc = 0
	}
	for d.acc == 0 && d.pos < len(d.in) {
		n := 0
		for n < 7 && d.pos < len(d.in) {
			d.acc = (d.acc << 8) | uint64(d.in[d.pos])
			d.pos++
			n++
		}
		fs += uint32(d.bitp)
		d.bitp = 8 * n // 绝对值，与 C 一致：acc 原本为 0，只有新字节有效
	}
	if d.acc == 0 {
		return fs // exhausted; degenerate case, the count check fails loudly later
	}
	i := bitsLen64(d.acc) - 1 // index of the highest set bit
	fs += uint32(d.bitp - i - 1)
	d.bitp = i
	return fs
}

// askFS (slow path): scan bit by bit for the next 1 bit; state fs keeps the
// zero count. Returns false at end of input.
func (d *ccsdsDecoder) askFS() bool {
	if !d.ask(1) {
		return false
	}
	for d.acc&(uint64(1)<<uint(d.bitp-1)) == 0 {
		if d.bitp == 1 {
			if d.pos >= len(d.in) {
				return false
			}
			d.acc = (d.acc << 8) | uint64(d.in[d.pos])
			d.pos++
			d.bitp += 8
		}
		d.fs++
		d.bitp--
	}
	return true
}

// dropFS consumes the terminating 1 bit and resets the counter.
func (d *ccsdsDecoder) dropFS() {
	d.fs = 0
	d.bitp--
}

func bitsLen64(v uint64) int {
	if v == 0 {
		return 0
	}
	return 64 - bits.LeadingZeros64(v) // exact "highest set bit + 1", matching C (64-clzll)
}

// --- sample output ----------------------------------------------------

// putSample appends one decoded (pre-inverse-preprocessing) sample to the
// RSI buffer.
func (d *ccsdsDecoder) putSample(s uint32) { d.rsiBuf[d.rsiPos] = s; d.rsiPos++ }

// copySample reads one raw bpv-bit sample (a reference sample / uncompressed).
// Mirrors libaec's copysample: input exhausted -> false.
func (d *ccsdsDecoder) copySample() bool {
	if !d.ask(d.params.BitsPerVal) {
		return false
	}
	d.putSample(d.getN(d.params.BitsPerVal))
	return true
}

// flush performs the inverse preprocessing on the pending RSI window (when
// pp is on) and appends the samples to the output. Mirrors libaec's
// FLUSH(KIND), minus byte serialization (we keep uint32 values).
func (d *ccsdsDecoder) flush() {
	flushEnd := d.rsiPos
	if d.pp {
		if d.flushStart == 0 && d.rsiPos > 0 {
			d.lastOut = d.rsiBuf[0]
			if d.signed {
				m := uint32(1) << (d.params.BitsPerVal - 1)
				d.lastOut = (d.lastOut ^ m) - m // sign-extend the reference sample
			}
			if !(d.want > 0 && len(d.out) >= d.want) {
				d.out = append(d.out, d.lastOut)
			}
			d.flushStart++
		}

		data := d.lastOut
		xmax := d.xmax
		if !d.signed {
			med := xmax/2 + 1
			for bp := d.flushStart; bp < flushEnd; bp++ {
				if d.want > 0 && len(d.out) >= d.want {
					return // output budget exhausted (C: avail_out runs out mid-flush)
				}
				v := d.rsiBuf[bp]
				halfD := (v >> 1) + (v & 1)
				var mask uint32
				if data&med != 0 {
					mask = xmax
				}
				if halfD <= mask^data {
					// zigzag residual: even v -> +v/2, odd v -> -(v+1)/2.
					// C: (d>>1)^(~((d&1)-1)) — the xor term is 0 for even v;
					// all ones for odd v, i.e. the uint32 two's-complement negation.
					data += (v >> 1) ^ (uint32(0) - (v & 1))
				} else {
					data = mask ^ v
				}
				d.out = append(d.out, data)
			}
			d.lastOut = data
		} else {
			for bp := d.flushStart; bp < flushEnd; bp++ {
				if d.want > 0 && len(d.out) >= d.want {
					return
				}
				v := d.rsiBuf[bp]
				halfD := (v >> 1) + (v & 1)
				if int32(data) < 0 {
					if halfD <= xmax+data+1 {
						data += (v >> 1) ^ (uint32(0) - (v & 1))
					} else {
						data = v - xmax - 1
					}
				} else {
					if halfD <= xmax-data {
						data += (v >> 1) ^ (uint32(0) - (v & 1))
					} else {
						data = xmax - v
					}
				}
				d.out = append(d.out, data)
			}
			d.lastOut = data
		}
	} else {
		for bp := d.flushStart; bp < flushEnd; bp++ {
			if d.want > 0 && len(d.out) >= d.want {
				return
			}
			d.out = append(d.out, d.rsiBuf[bp])
		}
	}
	d.flushStart = d.rsiPos
}

// resetRSI prepares the RSI buffer for the next interval (libaec's
// m_next_cds).
func (d *ccsdsDecoder) resetRSI() {
	if d.pp {
		d.ref = 1
		d.encodedBlockSize = d.blockSize - 1
	} else {
		d.ref = 0
		d.encodedBlockSize = d.blockSize
	}
}

// --- state machine modes (libaec decode.c, faithful port) -------------
// Convention: each mode does one unit of work; on success it sets d.nextMode
// and returns mContinue; input exhausted between units -> mExit; corruption
// -> mError. The driver in ccsdsDecode loops until exit/error.

func (d *ccsdsDecoder) mID() int {
	var id uint32
	if len(d.in)-d.pos >= d.inBlkLen {
		id = d.getN(d.idLen) // fast path: the whole block is certainly present
	} else if !d.ask(d.idLen) {
		return mExit // clean end of input between blocks
	} else {
		id = d.getN(d.idLen)
	}
	d.id = id
	d.nextMode = d.idTable[id]
	return mContinue
}

func (d *ccsdsDecoder) mNextCDS() int {
	if d.rsiPos == len(d.rsiBuf) {
		d.flush()
		d.flushStart = 0
		d.rsiPos = 0
		d.resetRSI() // interval boundary: the next block carries a reference sample (pp)
		if d.padRSI {
			d.bitp -= d.bitp % 8
		}
	} else {
		// libaec m_next_cds's else branch: only the first block of each RSI
		// interval carries a reference sample; the remaining blocks carry no
		// reference and encode the full block size.
		d.ref = 0
		d.encodedBlockSize = d.blockSize
	}
	d.nextMode = modeID
	return mContinue
}

func (d *ccsdsDecoder) mLowEntropy() int {
	if !d.ask(1) {
		return mExit
	}
	id := d.getN(1)
	d.id = id
	if id == 1 {
		if d.ref != 0 && !d.copySample() {
			return mExit
		}
		d.sampleCounter = d.ref
		d.nextMode = modeSESlow
		return mContinue
	}
	if d.ref != 0 && !d.copySample() {
		return mExit
	}
	d.nextMode = modeZeroBlock
	return mContinue
}

func (d *ccsdsDecoder) mSplit() int {
	k := int(d.id) - 1
	remaining := len(d.in) - d.pos
	if remaining >= d.inBlkLen {
		// fast path: the reference sample (pp), then all fs parts, then the k-bit parts
		if d.ref != 0 {
			d.putSample(d.getN(d.params.BitsPerVal))
		}
		for i := 0; i < d.encodedBlockSize; i++ {
			d.rsiBuf[d.rsiPos+i] = d.getFSFast() << uint(k)
		}
		if k > 0 {
			binaryPart := (k*d.encodedBlockSize)/8 + 9
			if len(d.in)-d.pos < binaryPart { // checked after the fs reads, as in C
				return mError
			}
			for i := 0; i < d.encodedBlockSize; i++ {
				d.rsiBuf[d.rsiPos+i] += d.getN(k)
			}
		}
		d.rsiPos += d.encodedBlockSize
		return d.mNextCDS()
	}
	// slow path: per sample, with input-exhaustion checks
	if d.ref != 0 && !d.copySample() {
		return mExit
	}
	d.sampleCounter = 0
	d.nextMode = modeSplitFS
	return mContinue
}

func (d *ccsdsDecoder) mSplitFS() int {
	k := int(d.id) - 1
	for d.sampleCounter < d.encodedBlockSize {
		if !d.askFS() {
			return mExit
		}
		d.rsiBuf[d.rsiPos+d.sampleCounter] = uint32(d.fs) << uint(k)
		d.dropFS()
		d.sampleCounter++
	}
	d.sampleCounter = 0 // C's m_split_fs resets before m_split_output
	d.nextMode = modeSplitOutput
	return mContinue
}

func (d *ccsdsDecoder) mSplitOutput() int {
	k := int(d.id) - 1
	for d.sampleCounter < d.encodedBlockSize {
		if k > 0 && !d.ask(k) {
			return mExit
		}
		if k > 0 {
			d.rsiBuf[d.rsiPos+d.sampleCounter] += d.getN(k)
		}
		d.sampleCounter++
	}
	d.rsiPos += d.encodedBlockSize
	return d.mNextCDS()
}

func (d *ccsdsDecoder) mZeroBlock() int {
	if !d.askFS() {
		return mExit
	}
	zeroBlocks := d.fs + 1
	d.dropFS()

	// Faithful port of libaec decode.c m_zero_block (src/decode.c:517-548):
	//   zero_blocks = state->fs + 1;            // ROS == 5
	//   if (zero_blocks == ROS)
	//       zero_blocks = MIN(strm->rsi - b, 64 - (b % 64));
	//   else if (zero_blocks > ROS) zero_blocks--;
	// where b = RSI_USED_SIZE / block_size = d.rsiPos / d.blockSize. The
	// reference keeps the ROS cap as 64 - (b%64) (not a hard RSI term), so
	// that is preserved verbatim; the first term comes from counting blocks
	// with "rsi - b", which only equals (rsi*blockSize - rsiPos)/blockSize
	// when rsiPos is block-aligned. mZeroBlock can be entered right after the
	// reference sample of the interval's first block was consumed (ref != 0),
	// in which case rsiPos is not necessarily aligned and the old
	// computation undercounted by one block; so use the block count exactly
	// as the reference does.
	if zeroBlocks == ccsdROS {
		b := d.rsiPos / d.blockSize
		zeroBlocks = minInt(d.params.RSI-b, 64-(b%64))
	} else if zeroBlocks > ccsdROS {
		zeroBlocks--
	}

	zeroSamples := zeroBlocks*d.blockSize - d.ref
	if len(d.rsiBuf)-d.rsiPos < zeroSamples {
		return mError
	}
	for i := 0; i < zeroSamples; i++ {
		d.rsiBuf[d.rsiPos+i] = 0
	}
	d.rsiPos += zeroSamples
	return d.mNextCDS()
}

func (d *ccsdsDecoder) mSESlow() int {
	for d.sampleCounter < d.blockSize {
		if !d.askFS() {
			return mExit
		}
		m := d.fs
		if m > 90 {
			return mError
		}
		d1 := int32(m) - d.seTable[2*m+1]
		if d.sampleCounter%2 == 0 {
			d.putSample(uint32(d.seTable[2*m] - d1))
			d.sampleCounter++
		}
		d.putSample(uint32(d1))
		d.sampleCounter++
		d.dropFS()
	}
	return d.mNextCDS()
}

func (d *ccsdsDecoder) mUncompCopy() int {
	for i := 0; i < d.blockSize; i++ {
		if !d.copySample() {
			return mExit
		}
	}
	return d.mNextCDS()
}

// createSETable builds the second-extension table (libaec's create_se_table):
// for m = 0..90, it stores pairs (i, ms) where i counts 0..12 and ms is the
// running k.
func createSETable(table *[182]int32) {
	k := 0
	for i := 0; i < 13; i++ {
		ms := k
		for j := 0; j <= i; j++ {
			table[2*k] = int32(i)
			table[2*k+1] = int32(ms)
			k++
		}
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- GRIB integration: template 5.42 decoder ---------------------------

// TemplateCCSDS is data-representation template 5.42 (grid point data -
// CCSDS recommended lossless compression).
const TemplateCCSDS = 42

// CcsdsPacking holds the template 5.42 header fields (section 5, 25 bytes
// total; the layout matches all three public AWS anchors byte for byte):
// NV@6-9, tmpl@10-11, R@12-15, B@16-17, D@18-19, bpv@20, typeOrig@21,
// flags@22, blockSize@23, rsi@24-25.
type CcsdsPacking struct {
	R          float32 // octets 12-15: reference value (pre-scaled by 10^D)
	B          int16   // octets 16-17: binary scale factor (sign-magnitude convention)
	D          int16   // octets 18-19: decimal scale factor (sign-magnitude)
	BitsPerVal uint8   // octet 20: bits per sample (0 = constant field, rejected)
	OrigType   uint8   // octet 21: type of original field values (table 5.1: 0=float, 1=int)
	Flags      uint8   // octet 22: ccsdsFlags (bit0 SIGNED ... bit5 PAD_RSI)
	BlockSize  uint8   // octet 23: samples per entropy block
	RSI        uint16  // octets 24-25: blocks per reference sample interval
}

// ccsdsUnpacker decodes template 5.42 end to end: the section 5 Walk
// supplies the header fields (field order per the spec table "5.42": 0=R
// F32, 1=B U16 smag, 2=D U16 smag, 3=bpv U8, 4=origType U8, 5=flags U8,
// 6=blockSize U8, 7=rsi U16), the AEC codec core above decodes the section 7
// bitstream, and the scaling follows eccodes strictly.
//
// Scaling semantics (two sources, read from eccodes master's
// src/eccodes/accessor/DataCcsdsPacking.cc unpack<T> (L497-515)):
// the codec output buffer is always reinterpreted as uint8/16/32_t* — even
// with the SIGNED flag set, no signed reinterpretation happens; SIGNED only
// changes the codec FSM (xmax/idLen), which the port above already mirrors.
// Then value = (sample * 2^B + R) * 10^(-D) (bscale/dscale via codes_power<T>).
//
// Open issue (bug.md §5.2): every CCSDS golden anchor in this repo uses
// flags = 14 (SIGNED = 0), so the SIGNED branch of flush() — which produces
// two's-complement wrapped uint32 values and sign-extends the reference
// sample — has no external eccodes anchor. That code is a line-by-line port
// of libaec decode.c (m_low_entropy_ref / FLUSH); no defensive range checks
// were added because any such check would change the (unanchored) decoded
// values; the shared getN bounds backstop protects it from out-of-bounds
// reads without changing results.
type ccsdsUnpacker struct{}

func (ccsdsUnpacker) Name() string { return "CCSDS recommended lossless compression (5.42)" }

func (ccsdsUnpacker) Unpack(ctx *Context) ([]float32, error) {
	if ctx.Fields == nil || ctx.Fields.Len() < 8 {
		return nil, fmt.Errorf("pack: template 5.42: section 5 fields missing or truncated (walk len %d, want 8)", walkLen(ctx.Fields))
	}
	f := ctx.Fields
	p := CcsdsPacking{
		R:          f.At(0).F,
		B:          int16(smInt16(uint16(f.At(1).U))),
		D:          int16(smInt16(uint16(f.At(2).U))),
		BitsPerVal: uint8(f.At(3).U),
		OrigType:   uint8(f.At(4).U),
		Flags:      uint8(f.At(5).U),
		BlockSize:  uint8(f.At(6).U),
		RSI:        uint16(f.At(7).U),
	}
	if p.BitsPerVal == 0 {
		// eccodes special case (DataCcsdsPacking.cc L457-462): bpv==0 returns
		// the unscaled reference value for every point — a constant-field
		// quirk with no calibration and no public AWS sample. Fail loud,
		// same policy as complex packing NG==0.
		return nil, fmt.Errorf("pack: template 5.42: bitsPerValue = 0 (constant field; eccodes returns unscaled R — not calibrated, refuse to guess)")
	}
	switch p.OrigType {
	case 0, 1: // table 5.1: float / int — the only defined values
	default:
		return nil, &ErrUnsupportedValue{Template: TemplateCCSDS, Field: "type of original field values", Value: int64(p.OrigType)}
	}

	twoB, tenMinusD, err := ccsdsScaleFactors(p.B, p.D)
	if err != nil {
		return nil, err
	}

	samples, err := ccsdsDecode(ccsdsParams{
		Flags:      p.Flags,
		BitsPerVal: int(p.BitsPerVal),
		BlockSize:  int(p.BlockSize),
		RSI:        int(p.RSI),
	}, ctx.Data, ctx.NumValues)
	if err != nil {
		return nil, err
	}

	out := make([]float32, len(samples))
	for i, s := range samples {
		// Samples are unsigned by construction (see the type comment); eccodes
		// does the same arithmetic in float/double precision.
		out[i] = float32((float64(s)*twoB + float64(p.R)) * tenMinusD)
	}
	return out, nil
}
