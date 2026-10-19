//
// xcheck_p6_b_complex — sections A/B: complex packing 5.3/5.2 decode
// cross-check against eccodes ground truth.
//
// Method : fixtures were
// generated on the Linux bench host by the system eccodes C API (python-eccodes 2.41.0) via
// gen_p6b.py — zero shared code path with the tested Go library. The oracle
// is eccodes' OWN readback of each file (codes_get_values, recorded in
// testdata/p6-b-xcheck/oracle_p6b.json with per-file sha256 and the exact
// encode parameters). Expected = that readback; the Go decode must match it
// BIT-EXACTLY as float32.
//
// Note on lossiness: eccodes auto-selects the binary scale factor E (and R)
// for complex packing, so arbitrary integer input vectors are not always
// representable — where the readback differs from the input, the readback is
// still the authoritative expected column (independent derivation; the input
// vector merely shapes the file). Constant fields and explicit-bits CCSDS
// cases are lossless and additionally checked against the input.

package pack_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	grib "github.com/knoweather/simple-grib"
)

type p6bOracleCase struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Grid   struct {
		Ni int `json:"Ni"`
		Nj int `json:"Nj"`
	} `json:"grid"`
	InputValues  []float64      `json:"input_values"`
	OracleValues []float64      `json:"oracle_values_eccodes_readback"`
	NPoints      int            `json:"n_points"`
	Sec5Keys     map[string]int `json:"sec5_keys"`
}

func p6bLoadOracle(t *testing.T) []p6bOracleCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "p6-b-xcheck", "oracle_p6b.json"))
	if err != nil {
		t.Fatalf("read oracle manifest: %v", err)
	}
	var doc struct {
		Cases []p6bOracleCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse oracle manifest: %v", err)
	}
	return doc.Cases
}

func p6bFind(t *testing.T, cases []p6bOracleCase, file string) p6bOracleCase {
	t.Helper()
	for _, c := range cases {
		if c.File == file {
			return c
		}
	}
	t.Fatalf("oracle manifest has no case %q", file)
	return p6bOracleCase{}
}

// p6bVerify decodes the fixture with the full library stack and asserts:
//   - fixture sha256 matches the manifest (generation side ≡ repo side);
//   - n values == oracle n_points;
//   - every value BIT-EXACT (float32) vs the eccodes readback column.
//
// When checkInput is true, the input vector is additionally required to match
// (lossless cases only).
func p6bVerify(t *testing.T, c p6bOracleCase, checkInput bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "p6-b-xcheck", c.File))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != c.SHA256 {
		t.Fatalf("fixture sha256 = %s, manifest says %s (generation side ≠ repo side)",
			hex.EncodeToString(sum[:]), c.SHA256)
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
	got, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(got) != c.NPoints {
		t.Fatalf("got %d values, oracle n_points = %d", len(got), c.NPoints)
	}
	for i, ov := range c.OracleValues {
		want := float32(ov)
		if math.Float32bits(got[i]) != math.Float32bits(want) {
			t.Errorf("value[%d] = %v (bits %08x), eccodes oracle = %v (bits %08x)",
				i, got[i], math.Float32bits(got[i]), want, math.Float32bits(want))
		}
	}
	if checkInput {
		for i, iv := range c.InputValues {
			want := float32(iv)
			if math.Float32bits(got[i]) != math.Float32bits(want) {
				t.Errorf("value[%d] = %v, input vector = %v (lossless case must match input)",
					i, got[i], want)
			}
		}
	}
}

// --- A: template 5.3 complex packing + spatial differencing -----------------

func TestP6B_A_Complex53(t *testing.T) {
	cases := p6bLoadOracle(t)
	t.Run("A1 order=2 noed auto", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "a1_cplx53_order2.grib2"), false)
	})
	t.Run("A2 order=1 (uncovered zone)", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "a2_cplx53_order1.grib2"), false)
	})
	t.Run("A3 order=2 + decimalScaleFactor", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "a3_cplx53_order2_d2.grib2"), false)
	})
	t.Run("A4 constant field (lossless)", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "a4_cplx53_constant.grib2"), true)
	})
	t.Run("A5 extreme magnitudes", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "a5_cplx53_extreme.grib2"), false)
	})
}

// --- B: template 5.2 plain complex packing ----------------------------------

func TestP6B_B_Complex52(t *testing.T) {
	cases := p6bLoadOracle(t)
	t.Run("B1 mixed integers", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "b1_cplx52_mixed.grib2"), false)
	})
	t.Run("B2 decimalScaleFactor=2", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "b2_cplx52_d2.grib2"), false)
	})
	t.Run("B3 constant field 15pt (lossless)", func(t *testing.T) {
		p6bVerify(t, p6bFind(t, cases, "b3_cplx52_constant.grib2"), true)
	})
}
