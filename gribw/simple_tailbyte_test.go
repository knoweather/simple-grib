// B-owned per §4 — this file added under the explicit fix assignment, disclosed)
package gribw

import (
	"encoding/hex"
	"testing"
)

// TestEncodeSimpleTailByteGolden pins the nbits>0 tail-byte branch of the
// simple-packing bitstream writer: total bits NOT a multiple of 8. The partial
// final group must occupy the HIGH bits of the last byte (MSB-first, contract
// §5.12) with low (8−nbits) zero padding — bug #2 wrote it
// unshifted (low bits), corrupting the final value for every non-byte-aligned
// field. Golden vector: 3 values × 5 bits = 15 bits → payload 0x08 0x86.
func TestEncodeSimpleTailByteGolden(t *testing.T) {
	sec5, payload, err := EncodeSimple([]float32{1, 2, 3}, SimpleParams{R: 0, E: 0, D: 0, Bits: 5})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	wantSec5 := "000000150500000003000000000000000000000500"
	if got, _ := hex.DecodeString(hex.EncodeToString(sec5)); len(got) != 21 {
		t.Fatalf("sec5 len = %d, want 21", len(got))
	}
	if s := hex.EncodeToString(sec5); s != wantSec5 {
		t.Fatalf("sec5 = %s, want %s", s, wantSec5)
	}
	// Stream: [00001][00010][00011] = 15 bits → byte1 = 00001000 (0x08),
	// byte2 = remaining 7 bits 1000011 left-shifted by 1 = 10000110 (0x86).
	// Bug #2 wrote the partial group unshifted: byte(cur) = 0x43.
	if s := hex.EncodeToString(payload); s != "0886" {
		t.Fatalf("payload = %s, want 0886 (tail byte must be 0x86: partial group 1000011 left-shifted by 1; bug #2 wrote 0x43)", s)
	}

	// Round-trip check: read the payload back MSB-first (the reader's rule,
	// ) and verify 15 bits = 00001|00010|00011 → {1,2,3}. This
	// is exactly what pack.simpleDecode does; done inline to avoid an
	// unexported cross-package call.
	var cur uint32
	nbits := 0
	var out []uint32
	for _, by := range payload {
		for b := 7; b >= 0; b-- {
			cur = cur<<1 | uint32((by>>uint(b))&1)
			nbits++
			if nbits == 5 {
				out = append(out, cur)
				cur, nbits = 0, 0
			}
		}
	}
	want := []uint32{1, 2, 3}
	if len(out) != len(want) {
		t.Fatalf("decoded %d groups, want %d", len(out), len(want))
	}
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("round-trip group[%d] = %d, want %d (tail byte misplacement)", i, out[i], want[i])
		}
	}
}
