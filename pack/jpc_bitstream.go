//
// 纯（非 MQ）位读取器与 tag tree，用于 Jasper 码流方言下的 JPEG 2000 包头：
// inclusion 与 numimsbs 树是用二元算术编码的 tag tree，码块长度增量采用一元
// "comma code"，而码块长度则是定长码。

package pack

// jpcBitReader reads bits from a byte buffer, honouring JPEG 2000 bit
// stuffing: after a 0xFF byte, a padding 0 bit follows and must be discarded.
type jpcBitReader struct {
	data    []byte
	pos     int
	cnt     int    // bits available in buf
	buf     uint32 // the most recently consumed bytes, low bits first
	eofFlag bool
}

func newJPCBitReader(data []byte, pos int) *jpcBitReader {
	return &jpcBitReader{data: data, pos: pos}
}

// getbit returns the next bit, or -1 on a hard error (end of the stream).
func (b *jpcBitReader) getbit() int {
	b.cnt--
	if b.cnt >= 0 {
		return int((b.buf >> uint(b.cnt)) & 1)
	}
	return b.fillbuf()
}

// fillbuf loads the next byte. It returns -1 as soon as the input is
// exhausted so the EOF signal reaches callers instead of masquerading as a
// data bit: the reference implementation returned 1 here, which turned every
// `if u < 0` check into dead code — truncated streams silently decoded
// garbage without any error (bug.md §2.9, fail-loud convention). Legal
// streams never read past the end, so for well-formed input the change has
// no effect at all.
func (b *jpcBitReader) fillbuf() int {
	if b.eofFlag {
		return -1
	}
	b.buf = (b.buf << 8) & 0xffff
	if b.pos >= len(b.data) {
		b.eofFlag = true
		return -1
	}
	c := b.data[b.pos]
	b.pos++
	if b.buf == 0xff00 {
		b.cnt = 6
	} else {
		b.cnt = 7
	}
	b.buf |= uint32(c) & ((1 << (b.cnt + 1)) - 1)
	return int((b.buf >> uint(b.cnt)) & 1)
}

func (b *jpcBitReader) eof() bool { return b.eofFlag }

func (b *jpcBitReader) getbits(n int) int {
	v := 0
	for k := n; k > 0; k-- {
		u := b.getbit()
		if u < 0 {
			return -1
		}
		v = (v << 1) | u
	}
	return v
}

// align advances to the next byte boundary, consuming stuffing bits.
func (b *jpcBitReader) align() {
	if b.cnt > 0 {
		b.getbits(b.cnt)
	} else if b.cnt == 0 && (b.buf&0xff) == 0xff {
		b.getbits(7)
	}
	if (b.buf & 0xff) == 0xff {
		b.getbits(7)
	}
}

// bytePos returns the codestream position after alignment to a byte boundary.
func (b *jpcBitReader) bytePos() int { return b.pos }

// jpcTagNode is one node of a tag tree.
type jpcTagNode struct {
	parent int // parent index, -1 for the root
	low    int
	value  int
}

// jpcTagTree is a two-dimensional tag tree over the code blocks of a
// precinct.
type jpcTagTree struct {
	numleafsh, numleafsv int
	nodes                []jpcTagNode
}

const jpcTagTreeMaxDepth = 32

// jpcTagTreeUnset is the "no value yet" sentinel held by freshly built tag
// tree nodes.
const jpcTagTreeUnset = 1 << 30

// newTagTree builds a tag tree with numleafsh columns and numleafsv rows of
// leaves, laid out level by level (leaves first). Parents are linked the
// same way as the reference implementation, including its handling of odd
// row/column counts.
func newTagTree(numleafsh, numleafsv int) *jpcTagTree {
	var nplh, nplv [jpcTagTreeMaxDepth]int
	numlvls := 0
	numnodes := 0
	nplh[0] = numleafsh
	nplv[0] = numleafsv
	for {
		n := nplh[numlvls] * nplv[numlvls]
		nplh[numlvls+1] = (nplh[numlvls] + 1) / 2
		nplv[numlvls+1] = (nplv[numlvls] + 1) / 2
		numnodes += n
		numlvls++
		if n <= 1 {
			break
		}
	}
	t := &jpcTagTree{numleafsh: numleafsh, numleafsv: numleafsv, nodes: make([]jpcTagNode, numnodes)}
	for i := range t.nodes {
		// The reference implementation resets each node to an unbounded
		// value; that is why decoding against a small threshold actually
		// reads a bit the first time instead of assuming the value is 0.
		t.nodes[i].value = jpcTagTreeUnset
	}

	node := 0
	parent := numleafsh * numleafsv
	parent0 := parent
	for i := 0; i < numlvls-1; i++ {
		for j := 0; j < nplv[i]; j++ {
			k := nplh[i]
			for {
				k--
				if k < 0 {
					break
				}
				t.nodes[node].parent = parent
				node++
				k--
				if k >= 0 {
					t.nodes[node].parent = parent
					node++
				}
				parent++
			}
			if (j&1) != 0 || j == nplv[i]-1 {
				parent0 = parent
			} else {
				parent = parent0
				parent0 += nplh[i]
			}
		}
	}
	t.nodes[node].parent = -1
	return t
}

// leaf returns the n-th leaf node.
func (t *jpcTagTree) leaf(n int) int { return n }

// decode decodes the value of the given leaf against the given threshold;
// it returns 1 when the value is below the threshold.
func (t *jpcTagTree) decode(leaf, threshold int, in *jpcBitReader) (int, error) {
	var stk [jpcTagTreeMaxDepth - 1]int
	stkptr := 0
	node := leaf
	for t.nodes[node].parent >= 0 {
		stk[stkptr] = node
		stkptr++
		node = t.nodes[node].parent
	}
	low := 0
	for {
		if low > t.nodes[node].low {
			t.nodes[node].low = low
		} else {
			low = t.nodes[node].low
		}
		for low < threshold && low < t.nodes[node].value {
			b := in.getbit()
			if b < 0 {
				return -1, &ErrJPCMalformed{Reason: "tag tree decode ran past the end of the packet header"}
			}
			if b != 0 {
				t.nodes[node].value = low
			} else {
				low++
			}
		}
		t.nodes[node].low = low
		if stkptr == 0 {
			break
		}
		stkptr--
		node = stk[stkptr]
	}
	if t.nodes[node].value < threshold {
		return 1, nil
	}
	return 0, nil
}

// jpcGetCommaCode reads a unary comma code (the number of 1 bits before the
// terminating 0 bit). Reading past the packet header is a hard error: getbit
// now reports EOF as -1 (bug.md §2.9).
func jpcGetCommaCode(in *jpcBitReader) (int, error) {
	n := 0
	for {
		v := in.getbit()
		if v < 0 {
			return -1, &ErrJPCMalformed{Reason: "comma code ran past the end of the packet header"}
		}
		if v == 0 {
			break
		}
		n++
	}
	return n, nil
}

// jpcGetNumNewPasses reads the truncated binary code for how many new coding
// passes this packet adds.
func jpcGetNumNewPasses(in *jpcBitReader) int {
	var n int
	if n = in.getbit(); n > 0 {
		if n = in.getbit(); n > 0 {
			if n = in.getbits(2); n == 3 {
				if n = in.getbits(5); n == 31 {
					if n = in.getbits(7); n >= 0 {
						n += 36 + 1
					}
				} else if n >= 0 {
					n += 5 + 1
				}
			} else if n >= 0 {
				n += 2 + 1
			}
		} else if n == 0 {
			n += 2
		}
	} else if n == 0 {
		n++
	}
	return n
}

// jpcFloorLog2 returns floor(log2(x)) for x > 0.
func jpcFloorLog2(x int) int {
	y := 0
	for x > 1 {
		x >>= 1
		y++
	}
	return y
}
