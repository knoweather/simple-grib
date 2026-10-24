package pack

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// pngSec5 builds a section 5 for template 5.41 (same field layout as 5.0).
func pngSec5(t *testing.T, numValues int, R float32, E, D int16, bits, origType uint8) []byte {
	t.Helper()
	sec5 := make([]byte, 21)
	binary.BigEndian.PutUint32(sec5[0:4], uint32(len(sec5)))
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], uint32(numValues))
	binary.BigEndian.PutUint16(sec5[9:11], uint16(TemplatePNG))
	binary.BigEndian.PutUint32(sec5[11:15], math.Float32bits(R))
	binary.BigEndian.PutUint16(sec5[15:17], pngSMU16(int32(E)))
	binary.BigEndian.PutUint16(sec5[17:19], pngSMU16(int32(D)))
	sec5[19] = bits
	sec5[20] = origType
	return sec5
}

func pngSMU16(v int32) uint16 {
	if v < 0 {
		return 0x8000 | uint16(-v)
	}
	return uint16(v)
}
func pngWalk(t *testing.T, sec5 []byte) *spec.Walk {
	t.Helper()
	def, ok := spec.Template(5, TemplatePNG)
	if !ok {
		t.Fatal("spec has no definition for template 5.41")
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	return w
}

func encodeGray8(t *testing.T, width, height int, samples []uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, width, height))
	copy(img.Pix, samples)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func encodeGray16(t *testing.T, width, height int, samples []uint16) []byte {
	t.Helper()
	img := image.NewGray16(image.Rect(0, 0, width, height))
	for i, v := range samples {
		binary.BigEndian.PutUint16(img.Pix[i*2:], v)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// encodeGray4 builds a 4-bit grayscale PNG by hand: image/png cannot emit
// sub-byte depths from an image.Image. rows holds packed nibbles per row.
func encodeGray4(t *testing.T, width int, rows [][]byte) []byte {
	t.Helper()
	var raw []byte
	for _, r := range rows {
		raw = append(raw, 0) // filter type: none
		raw = append(raw, r...)
	}
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(raw); err != nil {
		t.Fatalf("zlib write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zlib close: %v", err)
	}

	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(len(rows)))
	ihdr[8] = 4 // bit depth
	ihdr[9] = 0 // grayscale

	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, pngChunk("IHDR", ihdr)...)
	out = append(out, pngChunk("IDAT", z.Bytes())...)
	out = append(out, pngChunk("IEND", nil)...)
	return out
}

func pngChunk(typ string, data []byte) []byte {
	out := make([]byte, 0, 12+len(data))
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(data)))
	out = append(out, length[:]...)
	out = append(out, typ...)
	out = append(out, data...)
	crc := crc32.NewIEEE()
	crc.Write([]byte(typ))
	crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	return append(out, sum[:]...)
}

func TestPNG8Bit(t *testing.T) {
	raw := []uint8{0, 1, 7, 128, 255, 42, 17, 200, 9}
	sec5 := pngSec5(t, len(raw), 0, 0, 0, 8, 0)
	got, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           encodeGray8(t, 3, 3, raw),
		NumValues:      len(raw),
	})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for i, want := range raw {
		if got[i] != float32(want) {
			t.Errorf("value[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestPNG16Bit(t *testing.T) {
	raw := []uint16{0, 1, 255, 256, 65535, 32768}
	sec5 := pngSec5(t, len(raw), 0, 0, 0, 16, 0)
	got, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           encodeGray16(t, 3, 2, raw),
		NumValues:      len(raw),
	})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for i, want := range raw {
		if got[i] != float32(want) {
			t.Errorf("value[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestPNG4BitPackedRows(t *testing.T) {
	// width 3, two rows: [1 2 3] -> 0x12 0x30, [4 5 6] -> 0x45 0x60.
	pngBytes := encodeGray4(t, 3, [][]byte{{0x12, 0x30}, {0x45, 0x60}})
	sec5 := pngSec5(t, 6, 0, 0, 0, 4, 0)
	got, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           pngBytes,
		NumValues:      6,
	})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for i, want := range []uint32{1, 2, 3, 4, 5, 6} {
		if got[i] != float32(want) {
			t.Errorf("value[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestPNGScaledValues(t *testing.T) {
	// Same formula as 5.0: (raw×2^E + R) × 10^(−D).
	raw := []uint8{0, 1, 2, 3}
	sec5 := pngSec5(t, len(raw), 27315, 0, 2, 8, 0)
	got, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           encodeGray8(t, 2, 2, raw),
		NumValues:      len(raw),
	})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for i, want := range raw {
		w := float32((float64(want) + 27315.0) * 0.01)
		if got[i] != w {
			t.Errorf("value[%d] = %v, want %v", i, got[i], w)
		}
	}
}

func TestPNGRejectsColor(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i+3] = 0xFF
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	sec5 := pngSec5(t, 4, 0, 0, 0, 8, 0)
	_, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           buf.Bytes(),
		NumValues:      4,
	})
	if err == nil || !strings.Contains(err.Error(), "grayscale") {
		t.Fatalf("err = %v, want a grayscale-only rejection", err)
	}
}

func TestPNGRejectsBitsMismatch(t *testing.T) {
	sec5 := pngSec5(t, 4, 0, 0, 0, 16, 0) // says 16 bits, PNG is 8-bit
	_, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           encodeGray8(t, 2, 2, []uint8{1, 2, 3, 4}),
		NumValues:      4,
	})
	if err == nil || !strings.Contains(err.Error(), "PNG bit depth") {
		t.Fatalf("err = %v, want a bit-depth mismatch", err)
	}
}

func TestPNGRejectsShortImage(t *testing.T) {
	sec5 := pngSec5(t, 9, 0, 0, 0, 8, 0) // needs 9 samples, PNG has 4
	_, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           encodeGray8(t, 2, 2, []uint8{1, 2, 3, 4}),
		NumValues:      9,
	})
	if err == nil || !strings.Contains(err.Error(), "the field needs") {
		t.Fatalf("err = %v, want a short-image rejection", err)
	}
}

func TestPNGRejectsJunk(t *testing.T) {
	sec5 := pngSec5(t, 1, 0, 0, 0, 8, 0)
	_, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           append([]byte("not a png"), make([]byte, 32)...),
		NumValues:      1,
	})
	if err == nil || !strings.Contains(err.Error(), "not a PNG") {
		t.Fatalf("err = %v, want a PNG signature rejection", err)
	}
}

func TestPNGRejectsBitsAbove16(t *testing.T) {
	sec5 := pngSec5(t, 1, 0, 0, 0, 20, 0)
	_, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		NumValues:      1,
	})
	var bad *ErrUnsupportedValue
	if !errors.As(err, &bad) {
		t.Fatalf("err = %v (%T), want *ErrUnsupportedValue", err, err)
	}
	if bad.Template != TemplatePNG || bad.Field != "bitsPerValue" {
		t.Errorf("got {%d, %q}, want {%d, bitsPerValue}", bad.Template, bad.Field, TemplatePNG)
	}
}

func TestPNGPaddingIgnored(t *testing.T) {
	// A 2x2 image (4 samples) with numberOfValues = 3: only the first three
	// samples belong to the field.
	sec5 := pngSec5(t, 3, 0, 0, 0, 8, 0)
	got, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           encodeGray8(t, 2, 2, []uint8{1, 2, 3, 4}),
		NumValues:      3,
	})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for i, want := range []uint8{1, 2, 3} {
		if got[i] != float32(want) {
			t.Errorf("value[%d] = %v, want %v", i, got[i], want)
		}
	}
}

func TestPNGRejectsZeroValues(t *testing.T) {
	sec5 := pngSec5(t, 0, 0, 0, 0, 8, 0)
	_, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           encodeGray8(t, 1, 1, []uint8{1}),
		NumValues:      0,
	})
	if err == nil || !strings.Contains(err.Error(), "numberOfValues 0") {
		t.Fatalf("err = %v, want a numberOfValues-0 rejection", err)
	}
}

func TestPNGRejectsPalette(t *testing.T) {
	pal := color.Palette{color.Gray{Y: 0}, color.Gray{Y: 255}}
	img := image.NewPaletted(image.Rect(0, 0, 2, 2), pal)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	sec5 := pngSec5(t, 4, 0, 0, 0, 8, 0)
	_, err := Unpack(&Context{
		TemplateNumber: TemplatePNG,
		Fields:         pngWalk(t, sec5),
		Data:           buf.Bytes(),
		NumValues:      4,
	})
	if err == nil || !strings.Contains(err.Error(), "color type 3") {
		t.Fatalf("err = %v, want a palette/color-type rejection", err)
	}
}
