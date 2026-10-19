//
// real-value anchors : template 5.2
// messages carrying REAL ECMWF IFS 0p4-beta operational analysis values
// (t/850, gh/500; 900x451 reduced Gaussian, NV=405900), re-packed to
// tmpl=2 by eccodes 2.41 grib_set with value digests verified unchanged.
// Public AWS has no producer-written tmpl=2 (full-file census, internal
// item 1573①), so these are the "real values + authoritative encoding" anchors.
// Both carry group splitting (gs=1, NG=19293/20956) — the same structure
// as real NCEP 5.3 data (C6: NG=15900), exercising the shared /
// unpack path on production-scale input.
//
// Anchors = sha256 over big-endian float32 packing of eccodes 2.41
// grib_get_data values (11 significant digits round-trip float32 exactly).
// Independent cross-check: complex53-oracle.py replays both files with a
// hand-written WMO-FM94 decoder — 0/405900 mismatches, maxrelerr=0.

package grib

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
	"testing"
)

func TestB_Complex52RealValues(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		digest [32]byte
	}{
		{"C46 t850", "testdata/gribw-xcheck/complex_52_t850_real.grib2", mustHex(t, "25962aeebf662a9ae8fab7c6ba1eb9adf8f47c1360d8dccbb242f20a0d69089b")},
		{"C47 gh500", "testdata/gribw-xcheck/complex_52_gh500_real.grib2", mustHex(t, "7ab261315ed7fa7c47be083de76de8d21f84e6b260e84b0befe7e155911f22a7")},
	}
	const wantNV = 405900
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fl, err := Open(tc.path)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			defer fl.Close()
			m, err := fl.Message(0)
			if err != nil {
				t.Fatalf("Message(0): %v", err)
			}
			dr, err := m.DataRep()
			if err != nil {
				t.Fatalf("DataRep: %v", err)
			}
			if dr.TemplateNumber != 2 || dr.NumValues != wantNV {
				t.Fatalf("tmpl=%d NV=%d, want tmpl=2 NV=%d", dr.TemplateNumber, dr.NumValues, wantNV)
			}
			got, err := m.Values()
			if err != nil {
				t.Fatalf("Values: %v", err)
			}
			if len(got) != wantNV {
				t.Fatalf("len %d != %d", len(got), wantNV)
			}
			buf := make([]byte, 4*wantNV)
			for i, v := range got {
				binary.BigEndian.PutUint32(buf[4*i:], float32bits(v))
			}
			sum := sha256.Sum256(buf)
			if sum != tc.digest {
				t.Fatalf("value digest mismatch:\n got %x\nwant %x (eccodes 2.41 ground truth)", sum, tc.digest)
			}
		})
	}
}

func mustHex(t *testing.T, s string) [32]byte {
	t.Helper()
	var out [32]byte
	for i := 0; i < 32; i++ {
		hi, lo := hexval(s[2*i]), hexval(s[2*i+1])
		out[i] = hi<<4 | lo
	}
	return out
}

func hexval(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return 0
}

// float32bits = IEEE 754 bit pattern of f (big-endian packing below).
func float32bits(f float32) uint32 { return math.Float32bits(f) }
