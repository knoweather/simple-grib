// TWO'S-COMPLEMENT byte version of the negative-corner vector.
//
// WHY THIS FILE EXISTS: under the sign-magnitude convention adopted in
// , a two's-complement
// byte pattern like faa2b580 (-90e6 in two's complement) decodes to a
// sign-magnitude magnitude of ~2^31 micro-degrees (~2057 degrees). No real
// file uses such bytes; the A-B5 span check must reject them fail-loud. This
// fixture is the original byte layout, preserved verbatim (original md5
// 4858c4ef30c6a4d70c6dfee9f3b59837) so the out-of-range path stays tested
// and no historical bytes are lost.

package grib

import (
	"os"
	"strings"
	"testing"
)

func TestNegCornersTwosOutofRangeFailsLoud(t *testing.T) {
	f, err := os.Open("testdata/ecc-xcheck/negcorners_twos_outofrange.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	rdr, err := OpenReader(f, st.Size())
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer rdr.Close()
	m, err := rdr.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v (structure should be valid)", err)
	}

	_, err = m.Grid()
	if err == nil {
		t.Fatal("Grid(): expected fail-loud error for two's-complement bytes under the sign-magnitude convention")
	}
	// Di/Lo span check fires first: |Lo2-Lo1| = 2057.48 deg vs (Ni-1)*Di = 90.
	if !strings.Contains(err.Error(), "contradicts Lo1/Lo2 span") {
		t.Errorf("Grid() error = %q, want the A-B5 'contradicts Lo1/Lo2 span' fail-loud", err)
	}
}
