// for the simple-packing unpack fast paths . Keep these running in CI-like loops: a >2x regression on any
// row is a red flag.

package pack

import (
	"math/rand"
	"strconv"
	"testing"
)

// BenchmarkSimplePacking exercises each unpack path of simpleDecode with a
// deterministic pseudo-random bitstream: W=8/16/32 hit the byte-aligned fast
// paths, W=12 and W=7 hit the sliding-window path. numValues is sized so the
// hot loop dominates setup.
func BenchmarkSimplePacking(b *testing.B) {
	const n = 1 << 20 // ~1M values per round (4 MB output)
	for _, W := range []int{7, 8, 12, 16, 32} {
		W := W
		data := make([]byte, (W*n+7)/8)
		rnd := rand.New(rand.NewSource(int64(W))) // deterministic per width
		rnd.Read(data)
		p := SimplePacking{R: 273.15, E: -2, D: 1, Bits: uint8(W), OrigType: 0}
		b.Run(strconv.Itoa(W), func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				out, err := simpleDecode(p, data, n)
				if err != nil {
					b.Fatal(err)
				}
				if len(out) != n {
					b.Fatalf("len %d, want %d", len(out), n)
				}
			}
		})
	}
}

// TestSimpleDecodeLayoutGuard pins the bitstream layout contract the
// benchmarks rely on (ceil(numValues*W/8) bytes, MSB-first): a corrupt
// generator or a layout change would make the benchmarks meaningless.
func TestSimpleDecodeLayoutGuard(t *testing.T) {
	data := []byte{0xAB, 0xCD}
	p := SimplePacking{R: 0, E: 0, D: 0, Bits: 16, OrigType: 0}
	out, err := simpleDecode(p, data, 1)
	if err != nil {
		t.Fatal(err)
	}
	if out[0] != 43981 { // 0xABCD = 43981
		t.Fatalf("W=16 MSB-first layout broken: got %v", out[0])
	}
	data7 := []byte{0b10110_010, 0b1101_0000} // bits: 10110010 11010000
	p7 := SimplePacking{R: 0, E: 0, D: 0, Bits: 7, OrigType: 0}
	out7, err := simpleDecode(p7, data7, 2)
	if err != nil {
		t.Fatal(err)
	}
	// value 0 = first 7 bits = 1011001 = 89; value 1 = next 7 bits = 0110100 = 52
	if out7[0] != 89 || out7[1] != 52 {
		t.Fatalf("W=7 MSB-first layout broken: got %v", out7)
	}
}
