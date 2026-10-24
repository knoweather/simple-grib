package pack

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

// makeSec5 builds a synthetic section 5 byte slice (octet 1 based) for the
// given data representation template with simple-packing fields R/E/D/bits/
// origType at their WMO positions .
func makeSec5(tmpl uint16, nv uint32, R float32, E, D int16, bits, origType byte) []byte {
	b := make([]byte, 24)
	binary.BigEndian.PutUint32(b[0:4], uint32(len(b)))
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], nv)                    // octets 6-9: numberOfValues
	binary.BigEndian.PutUint16(b[9:11], tmpl)                 // octets 10-11: template number
	binary.BigEndian.PutUint32(b[11:15], math.Float32bits(R)) // octets 12-15: R (big-endian float32)
	binary.BigEndian.PutUint16(b[15:17], uint16(E))           // octets 16-17
	binary.BigEndian.PutUint16(b[17:19], uint16(D))           // octets 18-19
	b[19] = bits                                              // octet 20
	b[20] = origType                                          // octet 21
	return b
}

// TestUnpackDispatchSimple checks pack.Unpack end-to-end for template 5.0:
// section 5 fields come from a real spec.Walk, the payload is a bitstream.
// Parameters R=0, E=0, D=1, bits=8 make the round trip exact: raw = value*10.
func TestUnpackDispatchSimple(t *testing.T) {
	vals := []float32{1, 2, 3, 4}
	R, E, D, bits := float32(0), int16(0), int16(1), byte(8)
	payload := make([]byte, len(vals)) // 8 bits per value -> one byte each
	for k, v := range vals {
		payload[k] = byte(v * 10) // raw_k = (value*10^D - R)/2^E
	}
	sec5 := makeSec5(0, uint32(len(vals)), R, E, D, bits, 0)
	def, ok := spec.Template(5, 0)
	if !ok {
		t.Fatal("spec has no def for template 5.0")
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	ctx := &Context{TemplateNumber: 0, Fields: w, Data: payload, NumValues: len(vals)}
	got, err := Unpack(ctx)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for k := range vals {
		if got[k] != vals[k] {
			t.Errorf("got[%d] = %v, want %v", k, got[k], vals[k])
		}
	}
}

// TestUnpackDispatchIEEE checks template 5.4 through the registry.
func TestUnpackDispatchIEEE(t *testing.T) {
	payload := make([]byte, 8)
	binary.BigEndian.PutUint32(payload[0:4], 0x40490FDB) // ~3.14159
	binary.BigEndian.PutUint32(payload[4:8], 0xBF800000) // -1.0
	sec5 := make([]byte, 16)
	binary.BigEndian.PutUint32(sec5[0:4], 16)
	sec5[4] = 5
	binary.BigEndian.PutUint32(sec5[5:9], 2)  // numberOfValues
	binary.BigEndian.PutUint16(sec5[9:11], 4) // template 5.4
	sec5[11] = 1                              // precision 1 (binary32)
	def, ok := spec.Template(5, 4)
	if !ok {
		t.Fatal("spec has no def for template 5.4")
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	ctx := &Context{TemplateNumber: 4, Fields: w, Data: payload, NumValues: 2}
	got, err := Unpack(ctx)
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	wantPi := math.Float32frombits(0x40490FDB) // exact bits, not a decimal approximation
	if len(got) != 2 || got[0] != wantPi || got[1] != -1.0 {
		t.Errorf("got = %v, want [%v -1]", got, wantPi)
	}
}

// TestUnpackUnsupported checks the error for unregistered templates.
func TestUnpackUnsupported(t *testing.T) {
	ctx := &Context{TemplateNumber: 999}
	_, err := Unpack(ctx)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// TestPlaceholderDecoders checks the placeholder pattern still reports
// ErrDecoderNotConfigured. No built-in numeric template is a placeholder any
// more (5.40 ships a real decoder; 5.49152 is an image payload that refuses
// numeric decoding), so the entry is installed here.
func TestPlaceholderDecoders(t *testing.T) {
	const tmpl = 60000
	defer delete(registry, tmpl)
	Register(tmpl, placeholderUnpacker{codec: "test codec"})

	_, err := Unpack(&Context{TemplateNumber: tmpl})
	if err == nil {
		t.Fatalf("template %d: expected error", tmpl)
	}
	var ednc *ErrDecoderNotConfigured
	if !errors.As(err, &ednc) || ednc.Template != tmpl || ednc.Codec != "test codec" {
		t.Errorf("template %d: err = %v, want ErrDecoderNotConfigured{%d, test codec}", tmpl, err, tmpl)
	}

	if _, err := Unpack(&Context{TemplateNumber: TemplateJPEG2000}); err == nil {
		t.Fatalf("template 40: expected an error for an empty context")
	} else if errors.As(err, &ednc) {
		t.Errorf("template 40 still reports ErrDecoderNotConfigured: %v", err)
	}

	if _, err := Unpack(&Context{TemplateNumber: TemplateWebP}); err == nil {
		t.Fatalf("template 5.49152: expected an error for an empty context")
	} else {
		var notNumeric *ErrNotNumeric
		if !errors.As(err, &notNumeric) {
			t.Errorf("template 5.49152: err = %v, want *ErrNotNumeric", err)
		}
	}
}

// stubUnpacker is a test decoder returning one value.
type stubUnpacker struct{ n int }

func (s stubUnpacker) Name() string { return "stub" }

func (s stubUnpacker) Unpack(ctx *Context) ([]float32, error) { return []float32{float32(s.n)}, nil }

// TestRegisterOverride checks Register installs and replaces decoders.
func TestRegisterOverride(t *testing.T) {
	defer delete(registry, 7) // template 7 is unregistered by default
	Register(7, stubUnpacker{n: 42})
	got, err := Unpack(&Context{TemplateNumber: 7})
	if err != nil || len(got) != 1 || got[0] != 42 {
		t.Fatalf("got = %v (err %v), want [42]", got, err)
	}
	Register(7, stubUnpacker{n: 7}) // overwrite
	got, err = Unpack(&Context{TemplateNumber: 7})
	if err != nil || got[0] != 7 {
		t.Fatalf("after override got = %v (err %v), want [7]", got, err)
	}
}
