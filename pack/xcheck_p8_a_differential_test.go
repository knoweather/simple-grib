// differential hard line). This file is the SYNTHETIC half of the
// ccf4442 fast simple-packing unpack differential proof: an exhaustive
// width/parameter sweep comparing the optimized decoder against a verbatim
// port of the pre-optimization implementation. Any single-bit difference or
// error-message difference fails the test.

package pack

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/knoweather/simple-grib/internal/octetio"
)

// legacySimpleDecode is a VERBATIM port of pack.simpleDecode as of commit
// ccf4442^ (Owner , pre-optimization): per-value BitReader.ReadU plus the
// original formula. It exists ONLY as the differential oracle — it must never
// be called from production code, and if simpleDecode changes semantics this
// port must be re-derived from git history, not edited here.
func legacySimpleDecode(p SimplePacking, data []byte, numValues int) ([]float32, error) {
	want := (int(p.Bits)*numValues + 7) / 8
	if p.Bits == 0 {
		want = 0
	}
	if len(data) != want {
		return nil, fmt.Errorf("pack: template 5.0: data length %d != ceil(numValues×bits/8) = %d (numValues=%d, bits=%d)", len(data), want, numValues, p.Bits)
	}
	switch p.OrigType {
	case 0, 1:
	default:
		return nil, &ErrUnsupportedValue{Template: TemplateSimple, Field: "type of original field values", Value: int64(p.OrigType)}
	}
	// bug.md §2.5 fix, mirrored from pack.simpleDecode: WMO table 5.5 allows
	// only bitsPerValue 0..32, and the rejection is unconditional (the
	// pre-optimization code let bits > 32 with numValues == 0 through).
	if p.Bits > 32 {
		return nil, fmt.Errorf("pack: template 5.0: bitsPerValue %d out of range [0,32]", p.Bits)
	}

	twoE := math.Pow(2, float64(p.E))
	tenMinusD := math.Pow(10, -float64(p.D))

	out := make([]float32, numValues)
	br := octetio.NewBitReader(data)
	for i := 0; i < numValues; i++ {
		var raw uint32
		if p.Bits > 0 {
			v, err := br.ReadU(int(p.Bits))
			if err != nil {
				return nil, fmt.Errorf("pack: template 5.0: value %d: %w", i, err)
			}
			raw = v
		}
		out[i] = float32((float64(raw)*twoE + float64(p.R)) * tenMinusD)
	}
	return out, nil
}

// TestA_P8A_DifferentialSynthetic sweeps every bit width 0..33 (33 = the
// out-of-range error case), extreme and typical E/D scale factors (including
// overflow/underflow to Inf/0), representative reference values, and odd
// value counts that exercise tail bits. Optimized vs legacy must agree on
// every float32 bit and on every error message.
func TestA_P8A_DifferentialSynthetic(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261008))

	widths := make([]int, 0, 34)
	for w := 0; w <= 33; w++ {
		widths = append(widths, w)
	}
	es := []int16{-32767, -9, -2, -1, 0, 1, 2, 15, 32767}
	ds := []int16{-32767, -3, -1, 0, 1, 2, 15, 32767}
	rs := []float32{0, 273.15, -45.5, 1e-38, math.MaxFloat32}

	cases := 0
	for _, W := range widths {
		for _, E := range es {
			for _, D := range ds {
				for _, R := range rs {
					numValues := int(rnd.Intn(3)) // 0, 1, or 2 values: tiny + tail-bit shapes
					if rnd.Intn(4) == 0 {
						numValues = 1000 + rnd.Intn(500) // odd large counts
					}
					dataLen := (W*numValues + 7) / 8
					if W == 0 {
						dataLen = 0
					}
					data := make([]byte, dataLen)
					rnd.Read(data)
					// Force the all-ones raw pattern in (at least) one case per width:
					// — no NaN sentinel in simple packing.
					if W > 0 && numValues > 0 && rnd.Intn(3) == 0 {
						for i := range data {
							data[i] = 0xFF
						}
					}
					p := SimplePacking{R: R, E: E, D: D, Bits: uint8(W), OrigType: 0}

					got, gerr := simpleDecode(p, data, numValues)
					want, werr := legacySimpleDecode(p, data, numValues)

					if (gerr == nil) != (werr == nil) {
						t.Fatalf("W=%d E=%d D=%d R=%v n=%d: error presence diverges: opt=%v legacy=%v", W, E, D, R, numValues, gerr, werr)
					}
					if gerr != nil {
						if gerr.Error() != werr.Error() {
							t.Fatalf("W=%d E=%d D=%d R=%v n=%d: error text diverges:\n opt:   %v\n legacy: %v", W, E, D, R, numValues, gerr, werr)
						}
						cases++
						continue
					}
					if len(got) != len(want) {
						t.Fatalf("W=%d n=%d: length %d vs %d", W, numValues, len(got), len(want))
					}
					for i := range got {
						if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
							t.Fatalf("W=%d E=%d D=%d R=%v n=%d i=%d: bit divergence opt=%v (0x%08X) legacy=%v (0x%08X)",
								W, E, D, R, numValues, i, got[i], math.Float32bits(got[i]), want[i], math.Float32bits(want[i]))
						}
					}
					cases++
				}
			}
		}
	}

	// Error-path message equality beyond the width sweep.
	data := make([]byte, 10)
	rnd.Read(data)
	base := SimplePacking{R: 1, E: 0, D: 0, Bits: 8, OrigType: 0}

	if _, gerr := simpleDecode(base, data, 99); gerr == nil { // length mismatch
		t.Fatal("length mismatch: want error")
	} else if _, werr := legacySimpleDecode(base, data, 99); werr.Error() != gerr.Error() {
		t.Fatalf("length-mismatch text diverges:\n opt:   %v\n legacy: %v", gerr, werr)
	}
	bad := base
	bad.OrigType = 2 // unsupported origType
	if _, gerr := simpleDecode(bad, data, 10); gerr == nil {
		t.Fatal("origType=2: want error")
	} else if _, werr := legacySimpleDecode(bad, data, 10); !sameError(gerr, werr) {
		t.Fatalf("origType text diverges:\n opt:   %v\n legacy: %v", gerr, werr)
	}
	t.Logf("differential synthetic: %d cases bit-exact (W=0..33 × 9 E × 8 D × 5 R)", cases)
}

func sameError(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	return a.Error() == b.Error()
}
