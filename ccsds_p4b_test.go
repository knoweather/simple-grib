//
// 3b golden tests : end-to-end
// GRIB integration of template 5.42 — full message -> Message.Values() over
// the three public-AWS ECMWF anchors (aifs/ifs/wave; wave = C48 bitmap
// missing-value path), plus a hermetic synthetic-message test and the
// fail-loud matrix. The codec core itself is covered by pack/ccsds_golden_test.go
// (3a) and pack/ccsds_wave_test.go (B, C48); this file exercises the NEW 3b
// surface: template registration, section 5 Walk parsing (R/B/D smag/bpv/
// typeOrig/flags/block/rsi), scaling, and the Values() wiring.
//
// Digest chain ( 3b, FULL PRECISION — no text channel): eccodes 2.41 C API
// grib_get_double_array("values") -> exact doubles = (sample*2^B+R)*10^-D ->
// float32 (round-to-nearest) -> big-endian bytes -> md5 over all values.
// Anchors recorded on the Linux bench host 2026-10-06 via the system python gribapi binding
// (libeccodes 2.41.0, same version as B's conda build):
//   aifs  raw NV digest      1af2b5b81ec158005d68ee0c3d6c1dea (NV=1,038,240)
//   ifs   raw NV digest      77b23671252f2150d3bb78919faa775b  (NV=1,038,240)
//   wave  present-values     e73e02086ce7ce7a1f3366b9abf911f7  (NV=566,193)
//   wave  full-grid expanded bcd9d8d33e0cdc32efc7e594bcea3627  (1,038,240 pts,
//         bitmap-off positions filled with eccodes' missingValue 9999)
//
// Why NOT the %.10e text chain (ccsds-oracle.sh / 3a golden convention)? That
// chain prints eccodes' doubles with 11 significant digits and only then
// rounds to float32 — a double rounding that shifts ~1% of samples by exactly
// 1 ulp (ifs: text-chain anchor 15af366e... vs full-precision 77b23671...,
// measured 10,992/1,038,240 values at maxRel = 2^-23). Our reader computes
// the exact double and rounds ONCE, so it matches the full-precision anchor
// bit-exactly; the text chain remains valid at the SAMPLE level (3a), where
// both sides print identical doubles.
//
// Real files live outside the repo (size policy): resolved from
// /root/aws-ecmwf-samples/ on the Linux bench host or $CCSDS_SAMPLES_DIR; skip when absent.

package grib

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/knoweather/simple-grib/pack"
)

// --- e2e golden: three real anchors ------------------------------------------

type ccsdsE2E struct {
	name       string
	file       string
	fileMD5    string // whole-file md5 (ccsds-oracle.sh anchor)
	nv         int    // numberOfValues (sec5; present values, bitmap applied)
	total      int    // full grid points (0 = no section 6 bitmap)
	digest     string // eccodes 2.41 FULL-PRECISION float32-BE md5 over our NV values
	fullDigest string // (bitmap only) same digest over the full grid, bitmap-off
	// points filled with eccodes' missingValue 9999 — proves bit-exact sample
	// placement against eccodes' own bitmap interpretation.
}

var ccsdsE2Es = []ccsdsE2E{
	{name: "aifs025 (w)", file: "aifs025_oper.grib2",
		fileMD5: "66ff73d46d585b6bc67551b16a0efda1", nv: 1038240,
		digest: "1af2b5b81ec158005d68ee0c3d6c1dea"},
	{name: "ifs025 (slor)", file: "ifs025_oper.grib2",
		fileMD5: "786c65f4ff9cd32fd3b6aee7478f18c2", nv: 1038240,
		digest: "77b23671252f2150d3bb78919faa775b"},
	{name: "wave025 (mp2, C48 bitmap)", file: "wave025.grib2",
		fileMD5: "b6291dc44efc20339fe25eec70b8997d", nv: 566193, total: 1038240,
		digest:     "e73e02086ce7ce7a1f3366b9abf911f7",
		fullDigest: "bcd9d8d33e0cdc32efc7e594bcea3627"},
}

// ccsdsSamplesDir locates the real-file anchors; skips with a clear message
// when absent (they are large and kept on the Linux bench host, same convention as B's
// ccsds-oracle.sh which runs against /root/aws-ecmwf-samples).
func ccsdsSamplesDir(t *testing.T) string {
	t.Helper()
	candidates := []string{os.Getenv("CCSDS_SAMPLES_DIR"), "/root/aws-ecmwf-samples"}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(filepath.Join(c, "aifs025_oper.grib2")); err == nil && !fi.IsDir() {
			return c
		}
	}
	t.Skip("CCSDS real-file anchors absent: need aifs025_oper.grib2 in $CCSDS_SAMPLES_DIR or /root/aws-ecmwf-samples (the Linux bench host)")
	return ""
}

// ccsdsF32BEDigest is the FULL-PRECISION digest: float32 values -> big-endian
// bytes -> md5. No %.10e text round-trip (that channel double-rounds and shifts
// ~1% of samples by 1 ulp — see file header). This is exactly what the Linux bench host
// python gribapi anchor probe computes from eccodes' exact doubles.
func ccsdsF32BEDigest(vals []float32) string {
	h := md5.New()
	var buf [4]byte
	for _, v := range vals {
		u := math.Float32bits(v)
		buf[0] = byte(u >> 24)
		buf[1] = byte(u >> 16)
		buf[2] = byte(u >> 8)
		buf[3] = byte(u)
		h.Write(buf[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ccsdsExpandBitmap expands NV present values into the full grid using the
// section-6 bitmap (bitmap-off positions take the missingValue fill). Returns
// nil when message 0 has no section 6 carrying a bitmap (bitMapIndicator != 0)
// or is malformed. Walks ONLY message 0: bounded by sec0 totalLen (octets
// 9-16) and stopped at section 7 — raw may contain further messages/tail.
func ccsdsExpandBitmap(raw []byte, vals []float32, missing float32) []float32 {
	if len(raw) < 16 {
		return nil
	}
	totalLen := int(binary.BigEndian.Uint64(raw[8:16]))
	if totalLen > len(raw) {
		return nil // truncated message
	}
	pos := 16 // skip sec0
	for pos+5 <= totalLen {
		l := int(binary.BigEndian.Uint32(raw[pos : pos+4]))
		num := raw[pos+4]
		if l < 5 || pos+l > totalLen {
			return nil
		}
		if num == 6 {
			// bitMapIndicator (codetable 6.0): 0 = "a bit map applies to this
			// product and is specified in THIS section"; 1/254 = bitmap exists
			// but is not carried here; 255 = no bitmap. eccodes' G2BitmapPresent
			// accessor: bitmapPresent = (indicator != 255). Only indicator 0 has
			// the bitmap bytes in this section — the only case we can expand.
			if raw[pos+5] != 0 {
				return nil
			}
			bitmap := raw[pos+6 : pos+l] // octet-aligned, padded to whole bytes
			total := len(bitmap) * 8
			full := make([]float32, total)
			j := 0
			for i := 0; i < total; i++ {
				if bitmap[i/8]&(0x80>>(uint(i)%8)) != 0 {
					if j >= len(vals) {
						return nil // more set bits than values: malformed
					}
					full[i] = vals[j]
					j++
				} else {
					full[i] = missing
				}
			}
			if j != len(vals) {
				return nil // fewer set bits than values: malformed
			}
			return full
		}
		if num == 7 {
			break // last section of message 0 reached without a section 6
		}
		pos += l
	}
	return nil
}

func TestA_CcsdsGribEndToEnd(t *testing.T) {
	dir := ccsdsSamplesDir(t)
	for _, e := range ccsdsE2Es {
		e := e
		t.Run(e.name, func(t *testing.T) {
			path := filepath.Join(dir, e.file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			if got := md5Hex(raw); got != e.fileMD5 {
				t.Fatalf("fixture md5 = %s, want %s (wrong file)", got, e.fileMD5)
			}

			fl, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
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
			if dr.TemplateNumber != 42 {
				t.Fatalf("template = %d, want 42 (CCSDS)", dr.TemplateNumber)
			}
			if dr.NumValues != e.nv {
				t.Fatalf("numberOfValues = %d, want %d", dr.NumValues, e.nv)
			}

			got, err := m.Values()
			if err != nil {
				t.Fatalf("Values: %v", err)
			}
			if len(got) != e.nv {
				t.Fatalf("len(values) = %d, want %d (bitmap path must yield exactly NV values)", len(got), e.nv)
			}
			if digest := ccsdsF32BEDigest(got); digest != e.digest {
				t.Errorf("value-array digest = %s, want eccodes 2.41 full-precision anchor %s", digest, e.digest)
			}
			if e.total > 0 {
				// C48 bitmap path: expand our NV present values into the full
				// grid at the section-6 set-bit positions (scan order), filling
				// bitmap-off points with eccodes' missingValue (9999 for this
				// message) — must reproduce eccodes' own full-grid array.
				full := ccsdsExpandBitmap(raw, got, 9999.0)
				if full == nil {
					t.Fatalf("section 6 bitmap absent or inconsistent with NV=%d", e.nv)
				}
				if len(full) != e.total {
					t.Fatalf("full grid = %d points, want %d", len(full), e.total)
				}
				if digest := ccsdsF32BEDigest(full); digest != e.fullDigest {
					t.Errorf("full-grid (bitmap-expanded) digest = %s, want eccodes 2.41 anchor %s", digest, e.fullDigest)
				}
			}
		})
	}
}

// --- hermetic synthetic message ----------------------------------------------

// buildCCSDSMessage assembles a minimal but fully valid GRIB2 message with a
// template 5.42 section 5 (NV=9) and the given section 7 payload: sec0 + sec1
// + sec3 (regular_ll 3x3, La1=+60/Lo1=-90/La2=+40/Lo2=-70, Di=Dj=+10, scan
// 0x40 — same span-consistent layout as grid/negcorner_test.go) + sec4 + sec5
// + sec7 + the bare "7777" tail (real-world convention, tolerance).
// R = 100.0 (F32), B = -8 (smag 0x8008), D = 0, bpv/flags/block/rsi per the
// calibrated parameter combination; patchSec5 may override any sec5 field.
func buildCCSDSMessage(payload []byte, patchSec5 func(sec5 []byte)) []byte {
	sec1 := make([]byte, 21) // layout per identification.go: centre@Data[5:7], subCentre@[7:9], masterVer@[9], localVer@[10], year@[12:14]...
	binary.BigEndian.PutUint32(sec1[0:4], 21)
	sec1[4] = 1
	binary.BigEndian.PutUint16(sec1[5:7], 98) // centre (CCT-11)
	sec1[9] = 1                               // master table version
	binary.BigEndian.PutUint16(sec1[12:14], 2026)
	sec1[14], sec1[15], sec1[16] = 10, 6, 12 // month/day/hour (rest zero)

	sec3 := make([]byte, 100) // layout mirrors grid/definition_test.go makeSec3+fillBase
	binary.BigEndian.PutUint32(sec3[0:4], 100)
	sec3[4] = 3
	binary.BigEndian.PutUint16(sec3[12:14], 0) // template 3.0
	sec3[14] = 6                               // shape: sphere (table 3.2)
	putU32BE(sec3, 30, 3)                      // Ni
	putU32BE(sec3, 34, 3)                      // Nj
	putU32BE(sec3, 42, 0xFFFFFFFF)             // basicAngle/subdivisions missing -> microdegrees
	putSmagBE(sec3, 46, 60_000_000)            // La1 = +60 deg
	putSmagBE(sec3, 50, -90_000_000)           // Lo1 = -90 deg (sign-magnitude)
	sec3[54] = 0                               // resolution flags
	putSmagBE(sec3, 55, 40_000_000)            // La2 = +40 deg
	putSmagBE(sec3, 59, -70_000_000)           // Lo2 = -70 deg
	putSmagBE(sec3, 63, 10_000_000)            // Di = +10 deg (unsigned per WMO Note 45)
	putSmagBE(sec3, 67, 10_000_000)            // Dj = +10 deg
	sec3[71] = 0x40                            // scan mode: j scans positively

	sec4 := make([]byte, 33)
	binary.BigEndian.PutUint32(sec4[0:4], 33)
	sec4[4] = 4
	binary.BigEndian.PutUint16(sec4[7:9], 0) // product template 4.0

	sec5 := make([]byte, 25)
	binary.BigEndian.PutUint32(sec5[0:4], 25)
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], 9) // numberOfValues
	binary.BigEndian.PutUint16(sec5[9:11], 42)
	binary.BigEndian.PutUint32(sec5[11:15], math.Float32bits(100.0)) // R = 100.0 (pre-scaled by 10^D=1)
	binary.BigEndian.PutUint16(sec5[15:17], 0x8008)                  // B = -8 (sign-magnitude)
	binary.BigEndian.PutUint16(sec5[17:19], 0)                       // D = 0
	sec5[19] = 16                                                    // bitsPerValue
	sec5[20] = 0                                                     // typeOrig (table 5.1: float)
	sec5[21] = 14                                                    // ccsdsFlags (3BYTE|MSB|PREPROCESS)
	sec5[22] = 32                                                    // blockSize
	binary.BigEndian.PutUint16(sec5[23:25], 128)                     // rsi
	if patchSec5 != nil {
		patchSec5(sec5)
	}

	sec7 := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(sec7[0:4], uint32(len(sec7)))
	sec7[4] = 7
	copy(sec7[5:], payload)

	total := 16 + len(sec1) + len(sec3) + len(sec4) + len(sec5) + len(sec7) + 4
	var hdr [16]byte
	copy(hdr[0:5], "GRIB2")
	hdr[6] = 0 // discipline (table 0.0: atmospheric)
	hdr[7] = 2 // edition
	binary.BigEndian.PutUint64(hdr[8:16], uint64(total))
	msg := make([]byte, 0, total)
	msg = append(msg, hdr[:]...)
	msg = append(msg, sec1...)
	msg = append(msg, sec3...)
	msg = append(msg, sec4...)
	msg = append(msg, sec5...)
	msg = append(msg, sec7...)
	msg = append(msg, '7', '7', '7', '7') // bare tail ( real-world convention)
	return msg
}

func md5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func putU32BE(b []byte, off int, v uint32) { binary.BigEndian.PutUint32(b[off:off+4], v) }

func putSmagBE(b []byte, off int, v int32) {
	var raw uint32
	if v < 0 {
		raw = 0x80000000 | uint32(-v)
	} else {
		raw = uint32(v)
	}
	binary.BigEndian.PutUint32(b[off:off+4], raw)
}

// openSynthetic wraps a message body in an in-memory File.
func openSynthetic(t *testing.T, msg []byte) *Message {
	t.Helper()
	fl, err := OpenReader(io.NewSectionReader(bytes.NewReader(msg), 0, int64(len(msg))), int64(len(msg)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer fl.Close()
	m, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	return m
}

// TestA_CcsdsGribSynthetic is the hermetic end-to-end proof of the 3b wiring:
// a hand-built template 5.42 message whose AEC payload encodes NINE ZERO
// samples (bit layout, MSB-first: [id=0/4b][low-entropy sel=0/1b]
// [ref sample=0/16b][terminating 1 bit for the zero block at stream bit 21],
// i.e. bytes 00 00 04 — traced by hand against the 3a FSM: mID->mLowEntropy->
// copySample(0)->mZeroBlock writes 32-1=31 zeros, next mID hits end-of-input
// -> mExit -> flush with the NV budget stops after exactly 9 samples). Every
// decoded sample is therefore 0 and every value must be
// (0*2^-8 + 100.0)*10^0 = exactly 100.0.
func TestA_CcsdsGribSynthetic(t *testing.T) {
	m := openSynthetic(t, buildCCSDSMessage([]byte{0x00, 0x00, 0x04}, nil))

	dr, err := m.DataRep()
	if err != nil {
		t.Fatalf("DataRep: %v", err)
	}
	if dr.TemplateNumber != 42 || dr.NumValues != 9 {
		t.Fatalf("tmpl=%d NV=%d, want tmpl=42 NV=9", dr.TemplateNumber, dr.NumValues)
	}

	got, err := m.Values()
	if err != nil {
		t.Fatalf("Values: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("len(values) = %d, want 9", len(got))
	}
	for i, v := range got {
		if v != 100.0 {
			t.Errorf("value %d = %v, want exactly 100.0 (zero sample scaled by R)", i, v)
		}
	}
}

// --- fail-loud matrix ---------------------------------------------------------

func TestA_CcsdsGribFailLoud(t *testing.T) {
	// bpv = 0: eccodes constant-field quirk (returns unscaled R) — refused.
	m := openSynthetic(t, buildCCSDSMessage([]byte{0x00, 0x00, 0x08}, func(s []byte) { s[19] = 0 }))
	if _, err := m.Values(); err == nil || !strings.Contains(err.Error(), "bitsPerValue = 0") {
		t.Errorf("bpv=0: err = %v, want the constant-field refusal", err)
	}

	// typeOrig outside table 5.1 ({0,1}) -> ErrUnsupportedValue.
	m = openSynthetic(t, buildCCSDSMessage([]byte{0x00, 0x00, 0x08}, func(s []byte) { s[20] = 2 }))
	var usv *pack.ErrUnsupportedValue
	if _, err := m.Values(); !errors.As(err, &usv) || usv.Template != 42 || usv.Value != 2 {
		t.Errorf("typeOrig=2: err = %v, want *pack.ErrUnsupportedValue{42, 2}", err)
	}

	// blockSize = 0 -> codec parameter refusal.
	m = openSynthetic(t, buildCCSDSMessage([]byte{0x00, 0x00, 0x08}, func(s []byte) { s[22] = 0 }))
	if _, err := m.Values(); err == nil || !strings.Contains(err.Error(), "blockSize") {
		t.Errorf("blockSize=0: err = %v, want the codec parameter refusal", err)
	}

	// rsi = 0 -> codec parameter refusal.
	m = openSynthetic(t, buildCCSDSMessage([]byte{0x00, 0x00, 0x08}, func(s []byte) {
		binary.BigEndian.PutUint16(s[23:25], 0)
	}))
	if _, err := m.Values(); err == nil || !strings.Contains(err.Error(), "rsi") {
		t.Errorf("rsi=0: err = %v, want the codec parameter refusal", err)
	}

	// Truncated AEC stream (1 byte cannot encode 9 samples) -> loud failure.
	m = openSynthetic(t, buildCCSDSMessage([]byte{0x00}, nil))
	if _, err := m.Values(); err == nil || !strings.Contains(err.Error(), "stream truncated") {
		t.Errorf("truncated stream: err = %v, want the truncation refusal", err)
	}

	// Dispatch sanity: template 42 reaches the CCSDS unpacker, not ErrUnsupported.
	ctx := &pack.Context{TemplateNumber: 42, NumValues: 9}
	if _, err := pack.Unpack(ctx); err == nil || !strings.Contains(err.Error(), "template 5.42") {
		t.Errorf("Unpack(42, no fields): err = %v, want the 5.42 field-missing error (registration works)", err)
	}
}
