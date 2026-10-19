//
// 3a golden tests : full-field decode of the two ECMWF
// operational CCSDS samples against eccodes 2.41 ground truth recorded by
// B (pre-stage post 1444 + scripts/xcheck/ccsds-oracle.sh anchors).
//
// Ground-truth chain replicated exactly:
//   eccodes double value = (sample * 2^B + R) * 10^-D
//     -> grib_get_data prints with "%.10e" (tools/grib_get_data.cc L87)
//     -> B's oracle parses the printed text and packs float32 big-endian
//     -> md5 over all NV values = digest anchor.
// My decode must reproduce that digest bit-for-bit: any single sample off
// changes the hash.
//
// Fixtures: the AEC streams (sec7 data, octet 6 onward) live outside the
// repo by design (1.2MB + 0.6MB; same convention as B's ccsds-oracle.sh
// which runs against /root/aws-ecmwf-samples on the Linux bench host). Resolution order:
//   1. testdata/gribw-xcheck/ccsds_*_stream.bin (if ever committed)
//   2. $CCSDS_XCHECK_DIR
// 3. /root/ccsds-xcheck (the Linux bench host stable dir, created by
//      scripts/xcheck/ccsds-streams.sh from the two sample files)

package pack

import (
	"crypto/md5"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type ccsdsGolden struct {
	name       string
	streamFile string
	streamMD5  string
	rBits      uint32 // referenceValue float32 bits, big-endian as stored in sec5
	b          int    // binaryScaleFactor (smag-decoded from section 5)
	d          int    // decimalScaleFactor
	bpv        int
	flags      uint8
	blockSize  int
	rsi        int
	nv         int
	digest     string  // B's eccodes-2.41 anchor (see package comment)
	minVal     float64 // summary stats from B 1444, tolerance-checked
	maxVal     float64
}

var ccsdsGoldens = []ccsdsGolden{
	{
		name: "aifs025 (w)", streamFile: "ccsds_aifs_stream.bin",
		streamMD5: "cd29c999dbe477aca7e117e2f86ef4a4",
		rBits:     0xC06CA807, // -3.6977555751800537
		b:         -13, d: 0, bpv: 16, flags: 14, blockSize: 32, rsi: 128,
		nv:     1038240,
		digest: "1af2b5b81ec158005d68ee0c3d6c1dea",
		minVal: -3.6977555751800537, maxVal: 1.5391829,
	},
	{
		name: "ifs025 (slor)", streamFile: "ccsds_ifs_stream.bin",
		streamMD5: "476f20cbb5b93216398ee7ad8cf51b8d",
		rBits:     0x38D1AFFE, // ~1e-4
		b:         -18, d: 0, bpv: 16, flags: 14, blockSize: 32, rsi: 128,
		nv:     1038240,
		digest: "15af366e53c7e4e127489afa5a5d0b6a",
		maxVal: 0.2178772,
	},
}

// ccsdsFixtureDir locates the stream fixtures; skips with a clear message
// when absent (they are large and kept on the Linux bench host, see package comment).
func ccsdsFixtureDir(t *testing.T) string {
	t.Helper()
	candidates := []string{
		filepath.Join("..", "testdata", "gribw-xcheck"),
		os.Getenv("CCSDS_XCHECK_DIR"),
		"/root/ccsds-xcheck",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(c, "ccsds_aifs_stream.bin")); err == nil && !fi.IsDir() {
			return c
		}
	}
	t.Skip("CCSDS golden fixtures absent: need ccsds_{aifs,ifs}_stream.bin in testdata/gribw-xcheck/, $CCSDS_XCHECK_DIR, or /root/ccsds-xcheck (the Linux bench host; regenerate with scripts/xcheck/ccsds-streams.sh)")
	return ""
}

// ccsdsDigest replicates B's oracle digest chain over decoded samples:
// eccodes formula -> "%.10e" print -> parse -> float32 BE -> md5.
func ccsdsDigest(samples []uint32, r float64, b, d int) string {
	bscale := math.Pow(2, float64(b)) // exact for integer b
	dscale := math.Pow(10, -float64(d))
	h := md5.New()
	var buf [4]byte
	for _, s := range samples {
		v := (float64(s)*bscale + r) * dscale
		sv := strconv.FormatFloat(v, 'e', 10, 64) // grib_get_data: "%.10e"
		pv, _ := strconv.ParseFloat(sv, 64)
		u := math.Float32bits(float32(pv))
		buf[0] = byte(u >> 24)
		buf[1] = byte(u >> 16)
		buf[2] = byte(u >> 8)
		buf[3] = byte(u)
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestCcsdsGoldenDecode(t *testing.T) {
	dir := ccsdsFixtureDir(t)
	for _, g := range ccsdsGoldens {
		g := g
		t.Run(g.name, func(t *testing.T) {
			path := filepath.Join(dir, g.streamFile)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if got := md5Hex(data); got != g.streamMD5 {
				t.Fatalf("fixture md5 = %s, want %s (wrong file — regenerate with scripts/xcheck/ccsds-streams.sh)", got, g.streamMD5)
			}

			samples, err := ccsdsDecode(ccsdsParams{
				Flags: g.flags, BitsPerVal: g.bpv, BlockSize: g.blockSize, RSI: g.rsi,
			}, data, g.nv)
			if err != nil {
				t.Fatalf("ccsdsDecode: %v", err)
			}

			r := float64(math.Float32frombits(g.rBits))
			gotDigest := ccsdsDigest(samples, r, g.b, g.d)
			if gotDigest != g.digest {
				t.Errorf("value-array digest = %s, want eccodes anchor %s", gotDigest, g.digest)
			}

			// summary stats (B 1444), relative tolerance 1e-6
			minV, maxV := math.Inf(1), math.Inf(-1)
			for _, s := range samples {
				v := (float64(s)*math.Pow(2, float64(g.b)) + r) * math.Pow(10, -float64(g.d))
				if v < minV {
					minV = v
				}
				if v > maxV {
					maxV = v
				}
			}
			if g.minVal != 0 && !withinRel(minV, g.minVal, 1e-6) {
				t.Errorf("min = %v, want ~%v", minV, g.minVal)
			}
			if g.maxVal != 0 && !withinRel(maxV, g.maxVal, 1e-6) {
				t.Errorf("max = %v, want ~%v", maxV, g.maxVal)
			}
		})
	}
}

func withinRel(got, want float64, tol float64) bool {
	if want == 0 {
		return got == 0
	}
	d := got - want
	if d < 0 {
		d = -d
	}
	return d <= tol*math.Abs(want)
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

// TestCcsdsFailLoud covers the codec's loud-failure contract (no silent
// truncation, no out-of-range parameters).
func TestCcsdsFailLoud(t *testing.T) {
	// bpv == 0: constant-field quirk in eccodes (returns R unscaled) — we
	// refuse it, same policy as complex packing NG==0.
	if _, err := ccsdsDecode(ccsdsParams{Flags: 14, BitsPerVal: 0, BlockSize: 32, RSI: 128}, []byte{0}, 4); err == nil {
		t.Error("bpv=0 must fail loud")
	}
	if _, err := ccsdsDecode(ccsdsParams{Flags: 14, BitsPerVal: 33, BlockSize: 32, RSI: 128}, []byte{0}, 4); err == nil {
		t.Error("bpv=33 must fail loud")
	}
	if _, err := ccsdsDecode(ccsdsParams{Flags: 14, BitsPerVal: 16, BlockSize: 0, RSI: 128}, []byte{0}, 4); err == nil {
		t.Error("blockSize=0 must fail loud")
	}
	// truncated stream: fewer samples than numberOfValues -> loud failure
	if _, err := ccsdsDecode(ccsdsParams{Flags: 14, BitsPerVal: 16, BlockSize: 32, RSI: 128}, []byte{0x00, 0x00, 0x00}, 100); err == nil {
		t.Error("truncated stream must fail loud")
	}
}
