//
// xcheck_p6_b_ccsds — section C: CCSDS 5.42 AEC decode cross-check
// against eccodes ground truth (new parameter zone, avoiding the
// aifs/ifs/wave sample points).
//
// Same method as xcheck_p6_b_complex_test.go: the Linux bench host eccodes C API generated
// the fixtures (gen_p6b.py); expected = eccodes' own readback column in
// oracle_p6b.json; Go decode must match BIT-EXACTLY as float32.
//
// Scope note (disclosed in the report): the bitmap+CCSDS combination
// (original matrix C3) is DROPPED — (i) it is already covered by the
// accepted wave anchor (C48 bitmap missing-value path, bit-exact sample
// placement vs eccodes), and (ii) the Linux bench host SWIG binding cannot mark missing
// values for template 42 (NaN → garbage bits; same toolchain limitation A2
// hit in , board line 2241c). All-zero and explicit-bits cases below are
// lossless and additionally checked against the input vector.

package pack_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	grib "github.com/knoweather/simple-grib"
)

func TestP6B_C_Ccsds(t *testing.T) {
	cases := p6bLoadOracle(t)
	t.Run("C1 bits=8 mixed small integers (lossless)", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "c1_ccsds_bits8.grib2"), true)
	})
	t.Run("C2 bits=16 mixed magnitudes (lossless)", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "c2_ccsds_bits16.grib2"), true)
	})
	t.Run("C4 constant field: eccodes optimizes to bitsPerValue=0 -> our fail-loud refusal", func(t *testing.T) {
		// eccodes encodes a constant CCSDS field with bitsPerValue=0 (its own
		// readback returns the unscaled reference value). Our decoder is NOT
		// calibrated for that edge case and must refuse with a clear error —
		// this case cross-checks both sides' documented behavior.
		c := p6bFind(t, cases, "c4_ccsds_zeros.grib2")
		raw, err := os.ReadFile(filepath.Join("..", "testdata", "p6-b-xcheck", c.File))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		f, err := grib.OpenReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatalf("OpenReader: %v", err)
		}
		defer f.Close()
		m, err := f.Message(0)
		if err != nil {
			t.Fatalf("Message(0): %v", err)
		}
		if _, err := m.Values(); err == nil {
			t.Fatal("Values() succeeded on bitsPerValue=0 CCSDS; want fail-loud refusal")
		} else if !strings.Contains(err.Error(), "bitsPerValue = 0") || !strings.Contains(err.Error(), "constant field") {
			t.Errorf("error %q does not identify the constant-field refusal", err.Error())
		}
	})
	t.Run("C5 extreme magnitudes bits=16", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "c5_ccsds_extreme.grib2"), false)
	})
}
