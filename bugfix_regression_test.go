// Regression tests for the correctness fixes applied after the test
// engineer's bug report (bug.md). Each test asserts the fixed behaviour and
// cites the WMO/ecCodes evidence for the chosen semantics.

package grib

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// buildMessage assembles a minimal, fully-formed GRIB2 message: section 1
// (21B), section 3 (regular_ll 3×3, template 0), the supplied section 4 and
// section 5, a section 7 with the supplied payload, and the "7777" end
// marker (included in the section 0 total length).
func buildMessage(sec4, sec5, payload []byte) []byte {
	sec1 := make([]byte, 21)
	binary.BigEndian.PutUint32(sec1[0:4], 21)
	sec1[4] = 1
	binary.BigEndian.PutUint16(sec1[5:7], 98) // centre
	binary.BigEndian.PutUint16(sec1[12:14], 2026)
	sec1[14], sec1[15] = 1, 1 // month, day

	sec3 := make([]byte, 72)
	binary.BigEndian.PutUint32(sec3[0:4], 72)
	sec3[4] = 3
	sec3[14] = 6                                        // shape 6: R = 6371229 m
	binary.BigEndian.PutUint32(sec3[30:34], 3)          // Ni
	binary.BigEndian.PutUint32(sec3[34:38], 3)          // Nj
	binary.BigEndian.PutUint32(sec3[42:46], 0xFFFFFFFF) // subdivisions missing -> 1e-6 deg
	binary.BigEndian.PutUint32(sec3[46:50], 60_000_000) // La1
	binary.BigEndian.PutUint32(sec3[55:59], 40_000_000) // La2
	binary.BigEndian.PutUint32(sec3[59:63], 20_000_000) // Lo2
	binary.BigEndian.PutUint32(sec3[63:67], 10_000_000) // Di
	binary.BigEndian.PutUint32(sec3[67:71], 10_000_000) // Dj

	sec7 := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(sec7[0:4], uint32(len(sec7)))
	sec7[4] = 7
	copy(sec7[5:], payload)

	body := append(append(append(append([]byte{}, sec1...), sec3...), sec4...), sec5...)
	body = append(body, sec7...)
	total := 16 + len(body) + 4
	out := make([]byte, 16, total)
	copy(out[0:4], "GRIB")
	out[7] = 2
	binary.BigEndian.PutUint64(out[8:16], uint64(total))
	out = append(out, body...)
	out = append(out, '7', '7', '7', '7')
	return out
}

// sec4Template0 builds a 34-byte section 4 (product template 4.0) with the
// given forecast-time unit/raw count and first fixed-surface scale/value.
func sec4Template0(unit uint8, ftRaw uint32, lvScale byte, lvRaw uint32) []byte {
	b := make([]byte, 34)
	binary.BigEndian.PutUint32(b[0:4], 34)
	b[4] = 4
	binary.BigEndian.PutUint16(b[5:7], 0)       // NV
	binary.BigEndian.PutUint16(b[7:9], 0)       // template 4.0
	b[9] = 2                                    // octet 10: parameter category
	b[10] = 1                                   // octet 11: parameter number
	b[11] = 1                                   // octet 12: type of generating process
	b[17] = unit                                // octet 18: indicator of unit of time range
	binary.BigEndian.PutUint32(b[18:22], ftRaw) // octets 19-22: forecast time
	b[22] = 1                                   // octet 23: type of first fixed surface
	b[23] = lvScale                             // octet 24: scale factor
	binary.BigEndian.PutUint32(b[24:28], lvRaw) // octets 25-28: scaled value
	b[28] = 255                                 // octet 29: no second surface
	b[29] = 0
	binary.BigEndian.PutUint32(b[30:34], 0xFFFFFFFF)
	return b
}

// sec5Simple0 builds a 21-byte section 5 (simple packing, bits = 0 constant
// field, R = 0) declaring numValues values.
func sec5Simple0(numValues uint32) []byte {
	b := make([]byte, 21)
	binary.BigEndian.PutUint32(b[0:4], 21)
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], numValues)
	binary.BigEndian.PutUint16(b[9:11], 0) // template 5.0
	b[19] = 0                              // octet 20: bitsPerValue = 0
	return b
}

func openMessage(t *testing.T, msg []byte, opts ...Option) *Message {
	t.Helper()
	f, err := OpenReader(bytes.NewReader(msg), int64(len(msg)), opts...)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	return m
}

// ForecastTime must saturate instead of wrapping negative when raw × unit
// overflows int64 nanoseconds (bug.md §2.2).
func TestForecastTimeOverflowSaturates(t *testing.T) {
	// unit 0 = minute; raw = MaxUint32 → ~2.57e20 ns > MaxInt64.
	m := openMessage(t, buildMessage(sec4Template0(0, 0xFFFFFFFF, 0, 0), sec5Simple0(9), nil))
	p, err := m.Product()
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if p.ForecastTime == nil {
		t.Fatal("no forecast time")
	}
	if p.ForecastTime.Raw != 0xFFFFFFFF {
		t.Errorf("Raw = %d, want %d", p.ForecastTime.Raw, uint32(0xFFFFFFFF))
	}
	if !p.ForecastTime.Overflow {
		t.Errorf("Overflow = false, want true (the true duration exceeds time.Duration)")
	}
	if p.ForecastTime.Value < 0 {
		t.Errorf("Value = %v, want a non-negative saturated duration", p.ForecastTime.Value)
	}
}

// The first-fixed-surface scale factor is signed[1] sign-magnitude
// (ecCodes 4.horizontal.def + grib_decode_signed_long). 0x81 = -1, so a raw
// 850 is 8500 (bug.md §3.3, level-scale half).
func TestLevelScaleSignMagnitude(t *testing.T) {
	m := openMessage(t, buildMessage(sec4Template0(1, 0, 0x81, 850), sec5Simple0(9), nil))
	p, err := m.Product()
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if p.Level1 == nil || !p.Level1.HasValue {
		t.Fatalf("Level1 = %+v, want a value", p.Level1)
	}
	if p.Level1.Scale != 8500 {
		t.Errorf("Level1.Scale = %v, want 8500 (0x81 = sign-magnitude -1)", p.Level1.Scale)
	}
}

// 0xFF is the can_be_missing sentinel for the signed[1] scale factor, so the
// level carries a type but no numeric value.
func TestLevelScaleMissingSentinel(t *testing.T) {
	m := openMessage(t, buildMessage(sec4Template0(1, 0, 0xFF, 850), sec5Simple0(9), nil))
	p, err := m.Product()
	if err != nil {
		t.Fatalf("Product: %v", err)
	}
	if p.Level1 == nil {
		t.Fatal("Level1 absent")
	}
	if p.Level1.HasValue {
		t.Errorf("Level1.HasValue = true for a missing scale factor (0xFF), want false")
	}
}

// The section 8 end marker must be the literal "7777"; WithLenientEndSection
// relaxes that check (bug.md §3.6/§3.7).
func TestEndMarkerStrictness(t *testing.T) {
	msg := buildMessage(sec4Template0(1, 0, 0, 0), sec5Simple0(9), nil)
	msg = msg[:len(msg)-4] // drop the "7777"
	msg = append(msg, 'X', 'Y', 'Z')
	binary.BigEndian.PutUint64(msg[8:16], uint64(len(msg)))

	f, err := OpenReader(bytes.NewReader(msg), int64(len(msg)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer f.Close()
	if _, err := f.Message(0); err == nil {
		t.Errorf("Message(0) accepted a non-'7777' trailer; want a fail-loud error")
	}

	f2, err := OpenReader(bytes.NewReader(msg), int64(len(msg)), WithLenientEndSection())
	if err != nil {
		t.Fatalf("OpenReader(lenient): %v", err)
	}
	defer f2.Close()
	if _, err := f2.Message(0); err != nil {
		t.Errorf("WithLenientEndSection did not accept the trailer: %v", err)
	}
}

// DataRep must surface an octetio range error instead of silently reading a
// zero template number from a too-short section 5 (bug.md §2.4).
func TestDataRepShortSection5(t *testing.T) {
	short := make([]byte, 9) // template number (octets 10-11) does not fit
	binary.BigEndian.PutUint32(short[0:4], 9)
	short[4] = 5
	binary.BigEndian.PutUint32(short[5:9], 1)
	m := openMessage(t, buildMessage(sec4Template0(1, 0, 0, 0), short, nil))
	if _, err := m.DataRep(); err == nil {
		t.Errorf("DataRep on a 9-byte section 5: got nil, want a range error")
	}
}

// Walk.AtOk distinguishes "absent" from "zero"; At keeps its non-panicking
// zero-value contract (bug.md §2.3).
func TestWalkAtBounds(t *testing.T) {
	m := openMessage(t, buildMessage(sec4Template0(1, 0, 0, 0), sec5Simple0(9), nil))
	dr, err := m.DataRep()
	if err != nil {
		t.Fatalf("DataRep: %v", err)
	}
	if dr.Fields == nil {
		t.Fatal("no walk")
	}
	if _, ok := dr.Fields.AtOk(dr.Fields.Len()); ok {
		t.Errorf("AtOk(Len()) reported present")
	}
	if v := dr.Fields.At(dr.Fields.Len()); v != (spec.Value{}) {
		t.Errorf("At(Len()) = %+v, want the zero Value", v)
	}
}
