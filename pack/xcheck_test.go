package pack

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Binary differential cross-checks (对拍) against eccodes. The vectors in
// testdata/ecc-xcheck/ were produced on the Linux bench host by encoding real GRIB2 messages
// with eccodes 2.49.0 (conda-forge build): section 5/7 bytes are taken from
// the encoded message itself, and "eccodes_values" is eccodes' own decode of
// those same bytes (grib_get_double_array). The Go decoder must reproduce
// eccodes' output from the raw section bytes alone.
//
// Comparison semantics: eccodes decodes to float32 internally and returns
// doubles, so expected = float32(eccodesDouble) is exact. Simple-packing
// values are compared exact first; a mismatch within 1 ulp is accepted and
// reported (documented FP-semantics divergence between libm pow/rounding).
// IEEE vectors must be bit-exact.

const xcheckDir = "../testdata/ecc-xcheck"

// EccodesValues is []any because strict JSON cannot encode Infinity/NaN: the
// generator stores non-finite values as the strings "Inf"/"-Inf"/"NaN".
type xcheckSimple struct {
	Kind          string `json:"kind"`
	NumValues     int    `json:"numValues"`
	Section5Hex   string `json:"section5_hex"`
	Section7Hex   string `json:"section7_hex"`
	EccodesValues []any  `json:"eccodes_values"`
}

type xcheckIEEE struct {
	Kind          string `json:"kind"`
	NumValues     int    `json:"numValues"`
	Section5Hex   string `json:"section5_hex"`
	Section7Hex   string `json:"section7_hex"`
	EccodesValues []any  `json:"eccodes_values"`
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		switch x {
		case "Inf":
			return math.Inf(1)
		case "-Inf":
			return math.Inf(-1)
		case "NaN":
			return math.NaN()
		}
	}
	panic(fmt.Sprintf("unexpected eccodes value %v", v))
}

func readXcheck(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(xcheckDir, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return b
}

// ulpDiff32 returns the number of float32 ulps between a and b (0 if equal).
// NaN/Inf pairs compare by category; opposite infinities are far apart.
func ulpDiff32(a, b float32) int {
	ab := math.Float32bits(a)
	bb := math.Float32bits(b)
	if ab == bb {
		return 0
	}
	if math.IsNaN(float64(math.Float32frombits(ab))) || math.IsNaN(float64(math.Float32frombits(bb))) {
		return 1 // NaN vs anything: treat as adjacent (byte patterns may differ)
	}
	// map to monotonic integer ordering (two's-complement trick)
	m := func(u uint32) int64 {
		if u&0x80000000 != 0 {
			return int64(^u)
		}
		return int64(u)
	}
	d := m(ab) - m(bb)
	if d < 0 {
		d = -d
	}
	return int(d)
}

func TestXcheckSimple(t *testing.T) {
	names, err := listXcheck(xcheckDir)
	if err != nil {
		t.Fatal(err)
	}
	tested := 0
	for _, name := range names {
		if !strings.HasPrefix(name, "sp_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		var v xcheckSimple
		if err := json.Unmarshal(readXcheck(t, name), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v.Kind != "simple" {
			continue
		}

		// Re-parse section 5 from the raw bytes — deliberately NOT trusting
		// any R/E/D/bits fields stored in the JSON.
		s5, err := hex.DecodeString(v.Section5Hex)
		if err != nil {
			t.Fatalf("%s: bad section5_hex: %v", name, err)
		}
		nv := binary.BigEndian.Uint32(s5[5:9])
		if tpl := binary.BigEndian.Uint16(s5[9:11]); tpl != 0 {
			t.Fatalf("%s: unexpected data template %d", name, tpl)
		}
		R := math.Float32frombits(binary.BigEndian.Uint32(s5[11:15]))
		E := int16(binary.BigEndian.Uint16(s5[15:17]))
		D := int16(binary.BigEndian.Uint16(s5[17:19]))
		bits := s5[19]
		ot := s5[20]

		s7, err := hex.DecodeString(v.Section7Hex)
		if err != nil {
			t.Fatalf("%s: bad section7_hex: %v", name, err)
		}
		// section 7 = [len:4][no=7:1] + bitstream; verify the length field
		// against the stored bytes, then hand simpleDecode the data octets.
		if slen := binary.BigEndian.Uint32(s7[0:4]); int(slen) != len(s7) {
			t.Fatalf("%s: section 7 length field %d != actual %d", name, slen, len(s7))
		}
		if s7[4] != 7 {
			t.Fatalf("%s: section number octet = %d, want 7", name, s7[4])
		}
		data := s7[5:]

		got, err := simpleDecode(SimplePacking{R: R, E: E, D: D, Bits: bits, OrigType: ot}, data, int(nv))
		if err != nil {
			t.Fatalf("%s: simpleDecode: %v (R=%v E=%d D=%d bits=%d)", name, err, R, E, D, bits)
		}
		if len(got) != len(v.EccodesValues) {
			t.Fatalf("%s: got %d values, eccodes %d", name, len(got), len(v.EccodesValues))
		}

		exact := 0
		maxULP := 0
		worstIdx := -1
		for i := range got {
			want := float32(toFloat64(v.EccodesValues[i]))
			if math.Float32bits(got[i]) == math.Float32bits(want) {
				exact++
				continue
			}
			d := ulpDiff32(got[i], want)
			if d > maxULP {
				maxULP, worstIdx = d, i
			}
		}
		if maxULP > 1 {
			t.Errorf("%s: value %d: got %v (bits %08x), eccodes %v (bits %08x): diff %d ulp (>1)",
				name, worstIdx, got[worstIdx], math.Float32bits(got[worstIdx]),
				float32(toFloat64(v.EccodesValues[worstIdx])), math.Float32bits(float32(toFloat64(v.EccodesValues[worstIdx]))), maxULP)
			continue
		}
		tested++
		t.Logf("%s: bits=%d E=%d D=%d R=%v nv=%d: %d/%d exact, maxULP=%d", name, bits, E, D, R, nv, exact, len(got), maxULP)
	}
	if tested == 0 {
		t.Fatal("no simple-packing vectors found")
	}
}

func TestXcheckIEEE(t *testing.T) {
	var v xcheckIEEE
	b := readXcheck(t, "ieee_a.json")
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	s5, err := hex.DecodeString(v.Section5Hex)
	if err != nil {
		t.Fatalf("bad section5_hex: %v", err)
	}
	nv := binary.BigEndian.Uint32(s5[5:9])
	if tpl := binary.BigEndian.Uint16(s5[9:11]); tpl != 4 {
		t.Fatalf("unexpected data template %d", tpl)
	}
	prec := s5[11]
	s7, err := hex.DecodeString(v.Section7Hex)
	if err != nil {
		t.Fatalf("bad section7_hex: %v", err)
	}
	if slen := binary.BigEndian.Uint32(s7[0:4]); int(slen) != len(s7) {
		t.Fatalf("section 7 length field %d != actual %d", slen, len(s7))
	}
	data := s7[5:]

	got, err := ieeeDecode(IEEE{Precision: prec}, data, int(nv))
	if err != nil {
		t.Fatalf("ieeeDecode: %v", err)
	}
	if len(got) != len(v.EccodesValues) {
		t.Fatalf("got %d values, eccodes %d", len(got), len(v.EccodesValues))
	}
	for i := range got {
		want := float32(toFloat64(v.EccodesValues[i]))
		if math.Float32bits(got[i]) != math.Float32bits(want) {
			t.Errorf("ieee value %d: got %v (bits %08x), eccodes %v (bits %08x)",
				i, got[i], math.Float32bits(got[i]), want, math.Float32bits(want))
		}
	}
	t.Logf("ieee_a: %d/%d bit-exact", countExactIEEE(got, v.EccodesValues), len(got))
}

func countExactIEEE(got []float32, want []any) int {
	c := 0
	for i := range got {
		if math.Float32bits(got[i]) == math.Float32bits(float32(toFloat64(want[i]))) {
			c++
		}
	}
	return c
}

// listXcheck returns the JSON file names in dir.
func listXcheck(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read xcheck dir %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}
