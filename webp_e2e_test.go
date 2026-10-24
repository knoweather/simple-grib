package grib_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	simplewebp "github.com/knoweather/simple-webp"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/gribw"
	"github.com/knoweather/simple-grib/pack"
)

// webpTestImage is a small opaque image with distinct colours in every pixel.
func webpTestImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 17), G: uint8(y * 31), B: uint8(x + y), A: 0xFF})
		}
	}
	return img
}

// buildWebPMessage writes a GRIB2 message whose payload is webp, using the
// 3x3 fixture for the identification, grid and product sections. The image is
// 8x6 = 48 pixels, deliberately different from the 9 grid points.
func buildWebPMessage(t *testing.T, webp []byte) []byte {
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
	g, err := m.Grid()
	if err != nil {
		t.Fatalf("Grid: %v", err)
	}
	id, err := m.Identification()
	if err != nil {
		t.Fatalf("Identification: %v", err)
	}
	p, err := m.Product()
	if err != nil {
		t.Fatalf("Product: %v", err)
	}

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
	if err := msg.SetWebP(webp); err != nil {
		t.Fatalf("SetWebP: %v", err)
	}
	data, err := gribw.File(msg)
	if err != nil {
		t.Fatalf("File: %v", err)
	}
	return data
}

func readWebPMessage(t *testing.T, data []byte) *grib.Message {
	t.Helper()
	f, err := grib.OpenReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	m, err := f.Message(0)
	if err != nil {
		t.Fatalf("Message(0): %v", err)
	}
	f.Close()
	return m
}

func TestWebPRoundTrip(t *testing.T) {
	img := webpTestImage()
	webp, err := simplewebp.EncodeBytes(img, nil)
	if err != nil {
		t.Fatalf("EncodeBytes: %v", err)
	}
	m := readWebPMessage(t, buildWebPMessage(t, webp))

	raw, err := m.WebP()
	if err != nil {
		t.Fatalf("WebP: %v", err)
	}
	if !bytes.Equal(raw, webp) {
		t.Fatalf("WebP() returned %d bytes, want the %d written bytes verbatim", len(raw), len(webp))
	}

	info, err := m.WebPInfo()
	if err != nil {
		t.Fatalf("WebPInfo: %v", err)
	}
	if info.Width != 8 || info.Height != 6 || !info.Lossless || info.Animated {
		t.Errorf("WebPInfo = %+v, want {8 6 true false}", info)
	}

	decoded, err := m.WebPImage()
	if err != nil {
		t.Fatalf("WebPImage: %v", err)
	}
	got, ok := decoded.(*image.NRGBA)
	if !ok {
		t.Fatalf("WebPImage returned %T, want *image.NRGBA", decoded)
	}
	if got.Bounds() != img.Bounds() {
		t.Fatalf("bounds = %v, want %v", got.Bounds(), img.Bounds())
	}
	for y := 0; y < 6; y++ {
		for x := 0; x < 8; x++ {
			if got.NRGBAAt(x, y) != img.NRGBAAt(x, y) {
				t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got.NRGBAAt(x, y), img.NRGBAAt(x, y))
			}
		}
	}

	// The payload is an image, not a numeric field.
	_, err = m.Values()
	var notNumeric *pack.ErrNotNumeric
	if !errors.As(err, &notNumeric) {
		t.Fatalf("Values error = %v (%T), want *pack.ErrNotNumeric", err, err)
	}
	if notNumeric.Template != pack.TemplateWebP {
		t.Errorf("ErrNotNumeric.Template = %d, want %d", notNumeric.Template, pack.TemplateWebP)
	}
}

func TestWebPAcceptsLossyPayload(t *testing.T) {
	webp, err := simplewebp.EncodeLossyBytes(webpTestImage(), nil)
	if err != nil {
		t.Fatalf("EncodeLossyBytes: %v", err)
	}
	m := readWebPMessage(t, buildWebPMessage(t, webp))

	info, err := m.WebPInfo()
	if err != nil {
		t.Fatalf("WebPInfo: %v", err)
	}
	if info.Lossless {
		t.Error("WebPInfo.Lossless = true for a VP8 payload")
	}
	decoded, err := m.WebPImage()
	if err != nil {
		t.Fatalf("WebPImage: %v", err)
	}
	if decoded.Bounds().Dx() != 8 || decoded.Bounds().Dy() != 6 {
		t.Errorf("bounds = %v, want 8x6", decoded.Bounds())
	}
}

func TestSetWebPRejectsJunk(t *testing.T) {
	msg := gribw.New()
	if err := msg.SetWebP([]byte("not a webp file")); err == nil {
		t.Fatal("SetWebP accepted junk; want fail-loud")
	}
}

func TestWebPNumberOfValuesMustEqualPixels(t *testing.T) {
	webp, err := simplewebp.EncodeBytes(webpTestImage(), nil)
	if err != nil {
		t.Fatalf("EncodeBytes: %v", err)
	}

	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, _ := fl.Message(0)
	g, _ := m.Grid()
	id, _ := m.Identification()
	p, _ := m.Product()

	sec5 := make([]byte, 11)
	binary.BigEndian.PutUint32(sec5[0:4], 11)
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], 1) // wrong: 48 pixels
	binary.BigEndian.PutUint16(sec5[9:11], 49152)

	msg := gribw.New()
	_ = msg.SetDiscipline(m.Discipline())
	_ = msg.SetIdentification(id)
	_ = msg.SetGrid(g)
	_ = msg.SetProduct(p)
	if err := msg.SetDataSection(sec5, webp); err != nil {
		t.Fatalf("SetDataSection: %v", err)
	}
	_, err = gribw.File(msg)
	if err == nil || !strings.Contains(err.Error(), "pixels") {
		t.Fatalf("File error = %v, want a pixel-count mismatch", err)
	}
}

func TestWebPRequiresWebPTemplate(t *testing.T) {
	fl, err := grib.Open("testdata/gribw-xcheck/t20_grid_3x3.grib2")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer fl.Close()
	m, _ := fl.Message(0)

	if _, err := m.WebP(); err == nil || !strings.Contains(err.Error(), "not a WebP image") {
		t.Fatalf("WebP() error = %v, want a non-WebP-template error", err)
	}
}
