//
// 3a cross-check, third anchor : the
// wave025 mp2 sample — significant wave height, regular_ll 1440x721 WITH
// BITMAP (NV=566193 valid points). It is the only public-AWS CCSDS message
// with a bitmap and the third distinct real field in the parameter census
// (same codec parameters as aifs/ifs: bpv=16 flags=14 blockSize=32 rsi=128).
//
// This file deliberately does NOT touch 's ccsds_golden_test.go: it adds
// the C48 case in a separate B-owned *_test.go (lane split: B owns all
// *_test.go) so A's build-end artifact stays byte-stable. Digest chain is
// identical to A's golden test (sample -> (s*2^B+R)*10^-D -> "%.10e" print
// -> parse -> float32 BE -> md5), anchor recorded by B from eccodes 2.41
// grib_get_data on the Linux bench host (scripts/xcheck/ccsds-oracle.sh, 36/36 PASS).
//
// Fixture: ccsds_wave_stream.bin = sec7 octet 6 onward of wave025.grib2
// (md5 b6291dc44efc20339fe25eec70b8997d), 746496 bytes, stream md5
// f9e64fb16169f053b8948bfdd16c5dcc. Resolved by the same ccsdsFixtureDir
// helper as A's test (testdata/gribw-xcheck/, $CCSDS_XCHECK_DIR, or
// /root/ccsds-xcheck on the Linux bench host).

package pack

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestB_CcsdsWaveGolden(t *testing.T) {
	dir := ccsdsFixtureDir(t)
	const (
		streamFile = "ccsds_wave_stream.bin"
		streamMD5  = "f9e64fb16169f053b8948bfdd16c5dcc"
		nv         = 566193                             // bitmap: 566193 valid of 1440x721=1038240 grid points
		digest     = "9a97151ebd8cb507ea7c705e53a3d69e" // B's eccodes 2.41 anchor (C48)
		minVal     = 0.89437454939
		maxVal     = 13.101161659
	)
	data, err := os.ReadFile(filepath.Join(dir, streamFile))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if got := md5Hex(data); got != streamMD5 {
		t.Fatalf("fixture md5 = %s, want %s (wrong file — re-extract sec7 of wave025.grib2)", got, streamMD5)
	}

	samples, err := ccsdsDecode(ccsdsParams{Flags: 14, BitsPerVal: 16, BlockSize: 32, RSI: 128}, data, nv)
	if err != nil {
		t.Fatalf("ccsdsDecode: %v", err)
	}
	r := float64(math.Float32frombits(0x3F64F5BB)) // sec5 referenceValue raw
	gotDigest := ccsdsDigest(samples, r, -12, 0)   // B=-12 (smag 800c), D=0
	if gotDigest != digest {
		t.Errorf("value-array digest = %s, want eccodes anchor %s (C48 wave mp2)", gotDigest, digest)
	}

	minV, maxV := math.Inf(1), math.Inf(-1)
	for _, s := range samples {
		v := (float64(s)*math.Pow(2, -12) + r) * math.Pow(10, 0)
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if !withinRel(minV, minVal, 1e-6) {
		t.Errorf("min = %v, want ~%v", minV, minVal)
	}
	if !withinRel(maxV, maxVal, 1e-6) {
		t.Errorf("max = %v, want ~%v", maxV, maxVal)
	}
}
