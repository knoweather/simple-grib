package grib_test

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"math"
	"strings"
	"testing"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/gribw"
)

// pngGrayBytes encodes raw 8-bit grayscale samples as a PNG.
func pngGrayBytes(t *testing.T, width, height int, samples []uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	copy(img.Pix, samples)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// buildPNGMessage writes a template 5.41 message with the given PNG payload.
// The section 5 header comes from EncodeSimple (same field layout) with the
// template number switched to 41.
func buildPNGMessage(t *testing.T, numValues int, bits int, pngData []byte) []byte {
	t.Helper()
	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, err := fl.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	g, _ := m.Grid()
	id, _ := m.Identification()
	p, _ := m.Product()

	vals := make([]float32, numValues)
	for i := range vals {
		vals[i] = float32(i)
	}
	sec5, _, err := gribw.EncodeSimple(vals, gribw.SimpleParams{Bits: bits})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	binary.BigEndian.PutUint16(sec5[9:11], 41) // template 5.41 (PNG)

	msg := gribw.New()
	if err := msg.SetDiscipline(m.Discipline()); err != nil {
		t.Fatalf("SetDiscipline: %v", err)
	}
	if err := msg.SetIdentification(id); err != nil {
		t.Fatalf("SetIdentification: %v", err)
	}
	if err := msg.SetGrid(g); err != nil {
		t.Fatalf("SetGrid: %v", err)
	}
	if err := msg.SetProduct(p); err != nil {
		t.Fatalf("SetProduct: %v", err)
	}
	if err := msg.SetDataSection(sec5, pngData); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	data, err := gribw.File(msg)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	return data
}

func TestPNGEndToEnd(t *testing.T) {
	raw := []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8}
	data := buildPNGMessage(t, len(raw), 8, pngGrayBytes(t, 3, 3, raw))

	f, err := grib.OpenReader(bytes.NewReader(data), int64(len(data)))
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
	if len(got) != len(raw) {
		t.Fatalf("got %d values, want %d", len(got), len(raw))
	}
	for i, want := range raw {
		if got[i] != float32(want) {
			t.Errorf("value[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestPNGWriterRejectsShortCanvas(t *testing.T) {
	// 4 samples in the PNG, 9 declared values.
	pngData := pngGrayBytes(t, 2, 2, []uint8{1, 2, 3, 4})
	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, _ := fl.Message(0)
	g, _ := m.Grid()
	id, _ := m.Identification()
	p, _ := m.Product()
	vals := make([]float32, 9)
	sec5, _, err := gribw.EncodeSimple(vals, gribw.SimpleParams{Bits: 8})
	if err != nil {
		t.Fatalf("EncodeSimple: %v", err)
	}
	binary.BigEndian.PutUint16(sec5[9:11], 41)

	msg := gribw.New()
	_ = msg.SetDiscipline(m.Discipline())
	_ = msg.SetIdentification(id)
	_ = msg.SetGrid(g)
	_ = msg.SetProduct(p)
	if err := msg.SetDataSection(sec5, pngData); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	_, err = gribw.File(msg)
	if err == nil || !strings.Contains(err.Error(), "numberOfValues") {
		t.Fatalf("File error = %v, want a canvas/numberOfValues error", err)
	}
}

func buildPNGWriterMessage(t *testing.T, values []float32, bits int) []byte {
	t.Helper()
	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, _ := fl.Message(0)
	g, _ := m.Grid()
	id, _ := m.Identification()
	p, _ := m.Product()

	msg := gribw.New()
	if err := msg.SetDiscipline(m.Discipline()); err != nil {
		t.Fatalf("SetDiscipline: %v", err)
	}
	if err := msg.SetIdentification(id); err != nil {
		t.Fatalf("SetIdentification: %v", err)
	}
	if err := msg.SetGrid(g); err != nil {
		t.Fatalf("SetGrid: %v", err)
	}
	if err := msg.SetProduct(p); err != nil {
		t.Fatalf("SetProduct: %v", err)
	}
	if err := msg.SetPNG(values, gribw.SimpleParams{Bits: bits}); err != nil {
		t.Fatalf("SetPNG: %v", err)
	}
	data, err := gribw.File(msg)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	return data
}

func TestPNGWriterDepths(t *testing.T) {
	for _, tc := range []struct {
		bits   int
		values []float32
	}{
		{1, []float32{0, 1, 0, 1, 1, 0, 1, 0, 1}},
		{2, []float32{0, 1, 2, 3, 0, 2, 1, 3, 2}},
		{4, []float32{0, 1, 2, 3, 4, 5, 6, 7, 15}},
		{8, []float32{0, 1, 2, 3, 4, 5, 6, 7, 255}},
		{16, []float32{0, 1, 255, 256, 1000, 65535, 7, 8, 9}},
	} {
		data := buildPNGWriterMessage(t, tc.values, tc.bits)
		f, err := grib.OpenReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatalf("bits=%d: OpenReader: %v", tc.bits, err)
		}
		m, err := f.Message(0)
		if err != nil {
			t.Fatalf("bits=%d: Message: %v", tc.bits, err)
		}
		got, err := m.Values()
		f.Close()
		if err != nil {
			t.Fatalf("bits=%d: Values: %v", tc.bits, err)
		}
		for i, want := range tc.values {
			if got[i] != want {
				t.Errorf("bits=%d value[%d] = %v, want %v", tc.bits, i, got[i], want)
			}
		}
	}
}

func TestPNGWriterRejectsUnsupportedBits(t *testing.T) {
	for _, bits := range []int{0, 3, 5, 32} {
		_, _, err := gribw.EncodePNG([]float32{0}, gribw.SimpleParams{Bits: bits})
		if err == nil {
			t.Errorf("bits=%d: EncodePNG succeeded, want an error", bits)
		}
	}
}

func TestPNGWriterRejectsRawOutOfRange(t *testing.T) {
	if _, _, err := gribw.EncodePNG([]float32{300}, gribw.SimpleParams{Bits: 8}); err == nil {
		t.Error("raw out of range accepted; want fail-loud")
	}
	if _, _, err := gribw.EncodePNG([]float32{float32(math.Inf(1))}, gribw.SimpleParams{Bits: 8}); err == nil {
		t.Error("+Inf accepted; want fail-loud")
	}
}

// buildRawPNGMessage installs a caller-supplied section 5 + payload and builds
// the message; it returns the Build error instead of failing the test so the
// negative cases can assert on it.
func buildRawPNGMessage(t *testing.T, sec5, payload []byte) ([]byte, error) {
	t.Helper()
	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, _ := fl.Message(0)
	g, _ := m.Grid()
	id, _ := m.Identification()
	p, _ := m.Product()
	msg := gribw.New()
	_ = msg.SetDiscipline(m.Discipline())
	_ = msg.SetIdentification(id)
	_ = msg.SetGrid(g)
	_ = msg.SetProduct(p)
	if err := msg.SetDataSection(sec5, payload); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	return gribw.File(msg)
}

func TestPNGWriterRejectsColourPayload(t *testing.T) {
	sec5, _, err := gribw.EncodePNG(make([]float32, 9), gribw.SimpleParams{Bits: 8})
	if err != nil {
		t.Fatalf("EncodePNG: %v", err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 3, 3))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+3] = 0xFF
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	_, err = buildRawPNGMessage(t, sec5, buf.Bytes())
	if err == nil || !strings.Contains(err.Error(), "grayscale") {
		t.Fatalf("Build error = %v, want a grayscale rejection", err)
	}
}

func TestPNGWriterRejectsBitDepthMismatch(t *testing.T) {
	vals := make([]float32, 9)
	sec5, _, err := gribw.EncodePNG(vals, gribw.SimpleParams{Bits: 16})
	if err != nil {
		t.Fatalf("EncodePNG(16): %v", err)
	}
	_, payload8, err := gribw.EncodePNG(vals, gribw.SimpleParams{Bits: 8})
	if err != nil {
		t.Fatalf("EncodePNG(8): %v", err)
	}
	_, err = buildRawPNGMessage(t, sec5, payload8)
	if err == nil || !strings.Contains(err.Error(), "PNG bit depth") {
		t.Fatalf("Build error = %v, want a bit-depth mismatch", err)
	}
}
