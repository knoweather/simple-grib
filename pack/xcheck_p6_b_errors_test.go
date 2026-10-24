//
// xcheck_p6_b_errors — section E: pack registry & error-path cross-check.
//
// Method : hand-assembled
// minimal GRIB2 / section-5 byte vectors, fully deterministic — no external
// oracle is needed because the expected outcome is the exact error type and
// fields themselves .
//
// Two levels are checked per case:
//   - pack level:  pack.Unpack(&Context{...}) — the direct trigger;
//   - e2e level:   full message → Message.Values() — how the error surfaces
//     to a library user (datrep.go converts unregistered-template failures
//     into *grib.ErrUnknownTemplate, lines 118-123).
//
// Section byte layout per frozen contract (section.go header comment +
// datrep.go): Data[0:4] = 4-octet section length, Data[4] = section number,
// Data[5:] = content from octet 6; a WMO "octet N" field lives at Data[N-1].

package pack_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"

	grib "github.com/knoweather/simple-grib"
	"github.com/knoweather/simple-grib/pack"
	"github.com/knoweather/simple-grib/spec"
)

// ieeeSec5 builds a template 5.4 section 5 (12 octets, minimum) with the
// given precision at octet 12 and NV=9.
func p6bIEEEsec5(precision byte) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint32(b[0:4], 12)
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], 9)  // NV @ octets 6-9
	binary.BigEndian.PutUint16(b[9:11], 4) // template 5.4 @ octets 10-11
	b[11] = precision                      // octet 12 (table 5.7)
	return b
}

// complexSec5 builds a template 5.3 section 5 (49 octets) with the given
// order of spatial differencing (octet 48), missing value management
// (octet 23) and numberOfGroups (octets 32-35); all other fields zero.
func p6bComplexSec5(order, missingMgmt byte, ng uint32) []byte {
	b := make([]byte, 49)
	binary.BigEndian.PutUint32(b[0:4], 49)
	b[4] = 5
	binary.BigEndian.PutUint32(b[5:9], 9)  // NV @ octets 6-9
	binary.BigEndian.PutUint16(b[9:11], 3) // template 5.3 @ octets 10-11
	// octet 12-15 R=0, 16-17 E=0, 18-19 D=0, 20 bits=0, 21 origType=0: already zero
	b[22] = missingMgmt                      // octet 23 (table 5.5)
	binary.BigEndian.PutUint32(b[31:35], ng) // NG @ octets 32-35
	b[47] = order                            // octet 48 (table 5.6)
	// octet 49 noed = 0 (zero)
	return b
}

func p6bWalk(t *testing.T, tmpl int, sec5 []byte) *spec.Walk {
	t.Helper()
	def, ok := spec.Template(5, tmpl)
	if !ok {
		t.Fatalf("no spec definition for data representation template %d", tmpl)
	}
	w, err := spec.NewWalk(def, sec5)
	if err != nil {
		t.Fatalf("NewWalk: %v", err)
	}
	return w
}

// --- E1-E3: unregistered templates -> pack.ErrUnsupported (pack level) -----

func TestP6B_ErrUnsupported_UnregisteredTemplates(t *testing.T) {
	cases := []struct {
		name string
		tmpl int // WMO specdata name for the report
	}{
		{"5.1 matrix values (Experimental)", 1},
		{"5.50 spectral coefficients", 50},
		{"5.200 run-length packing", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := pack.Unpack(&pack.Context{TemplateNumber: c.tmpl})
			if !errors.Is(err, pack.ErrUnsupported) {
				t.Fatalf("want errors.Is(err, pack.ErrUnsupported), got %v", err)
			}
			if want := "pack: unsupported data representation template"; !bytes.Contains([]byte(err.Error()), []byte(want)) {
				t.Errorf("error message %q does not contain sentinel text %q", err.Error(), want)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(itoa(c.tmpl))) {
				t.Errorf("error message %q does not carry template number %d", err.Error(), c.tmpl)
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// --- E4-E5: placeholder decoders -> *pack.ErrDecoderNotConfigured ----------

// p6bPlaceholder is a caller-installed placeholder: the public Register path
// must keep surfacing *pack.ErrDecoderNotConfigured for codecs a user chooses
// to leave unimplemented.
type p6bPlaceholder struct{ codec string }

func (p p6bPlaceholder) Name() string { return p.codec }

func (p p6bPlaceholder) Unpack(ctx *pack.Context) ([]float32, error) {
	return nil, &pack.ErrDecoderNotConfigured{Template: ctx.TemplateNumber, Codec: p.codec}
}

func TestP6B_ErrDecoderNotConfigured_Placeholders(t *testing.T) {
	const tmpl = 60002
	pack.Register(tmpl, p6bPlaceholder{codec: "test codec"})
	// Payload content is irrelevant: the placeholder fails before touching it.
	_, err := pack.Unpack(&pack.Context{TemplateNumber: tmpl, Data: []byte{0, 1, 2}})
	var e *pack.ErrDecoderNotConfigured
	if !errors.As(err, &e) {
		t.Fatalf("want *pack.ErrDecoderNotConfigured, got %T: %v", err, err)
	}
	if e.Template != tmpl || e.Codec != "test codec" {
		t.Errorf("got {Template: %d, Codec: %q}, want {Template: %d, Codec: %q}",
			e.Template, e.Codec, tmpl, "test codec")
	}

	// Template 5.49152 (WebP) is an image payload: numeric decoding must
	// refuse with *pack.ErrNotNumeric instead of claiming a missing codec.
	if _, err := pack.Unpack(&pack.Context{TemplateNumber: 49152}); err == nil {
		t.Fatal("template 49152: expected an error for an empty context")
	} else {
		var notNumeric *pack.ErrNotNumeric
		if !errors.As(err, &notNumeric) {
			t.Errorf("template 49152: err = %v, want *pack.ErrNotNumeric", err)
		}
	}
}

// --- E6: IEEE precision != 1 -> *pack.ErrUnsupportedValue -------------------

func TestP6B_ErrUnsupportedValue_IEEEPrecision(t *testing.T) {
	for _, prec := range []byte{0, 2, 3} { // table 5.7 values other than 1
		t.Run("precision="+itoa(int(prec)), func(t *testing.T) {
			w := p6bWalk(t, 4, p6bIEEEsec5(prec))
			payload := make([]byte, 9*4) // NV=9 × 4 bytes (irrelevant: rejected before decode)
			_, err := pack.Unpack(&pack.Context{TemplateNumber: 4, Fields: w, Data: payload, NumValues: 9})
			var e *pack.ErrUnsupportedValue
			if !errors.As(err, &e) {
				t.Fatalf("want *pack.ErrUnsupportedValue, got %T: %v", err, err)
			}
			if e.Template != pack.TemplateIEEE || e.Field != "precision" || e.Value != int64(prec) {
				t.Errorf("got {Template: %d, Field: %q, Value: %d}, want {Template: %d, Field: \"precision\", Value: %d}",
					e.Template, e.Field, e.Value, pack.TemplateIEEE, prec)
			}
		})
	}
}

// --- E7: complex packing unsupported-value paths ----------------------------

func TestP6B_ErrUnsupportedValue_Complex(t *testing.T) {
	t.Run("order=3 (table 5.6 allows only 1 or 2)", func(t *testing.T) {
		w := p6bWalk(t, 3, p6bComplexSec5(3, 0, 1))
		_, err := pack.Unpack(&pack.Context{TemplateNumber: 3, Fields: w, Data: []byte{}, NumValues: 9})
		var e *pack.ErrUnsupportedValue
		if !errors.As(err, &e) {
			t.Fatalf("want *pack.ErrUnsupportedValue, got %T: %v", err, err)
		}
		if e.Template != pack.TemplateComplex || e.Field != "order of spatial differencing" || e.Value != 3 {
			t.Errorf("got {Template: %d, Field: %q, Value: %d}", e.Template, e.Field, e.Value)
		}
	})
	t.Run("missingMgmt=1 (table 5.5; only 0 calibrated)", func(t *testing.T) {
		w := p6bWalk(t, 3, p6bComplexSec5(0, 1, 1))
		_, err := pack.Unpack(&pack.Context{TemplateNumber: 3, Fields: w, Data: []byte{}, NumValues: 9})
		var e *pack.ErrUnsupportedValue
		if !errors.As(err, &e) {
			t.Fatalf("want *pack.ErrUnsupportedValue, got %T: %v", err, err)
		}
		if e.Template != pack.TemplateComplex || e.Field != "missing value management used" || e.Value != 1 {
			t.Errorf("got {Template: %d, Field: %q, Value: %d}", e.Template, e.Field, e.Value)
		}
	})
}

// --- E8: end-to-end wiring — unregistered templates via Values() ----------
//
// Patches the data representation template number of a known-good in-repo
// fixture (complex_52_plain.grib2, NV=9 on a 9-point grid). The message stays
// structurally valid; only the template dispatch changes. Two surfaces are
// documented (datrep.go lines 118-123):
//   - E8a: template with NO spec definition in this build (5.10 — not in
//     specdata) → Fields == nil → *grib.ErrUnknownTemplate{Section: 5, ...};
//   - E8b: template WITH a spec definition but no registered pack decoder
//     (5.200 run-length — has a specdata CSV, absent from the pack registry)
//     → Fields != nil → the raw wrapped pack.ErrUnsupported surfaces.

func TestP6B_ErrUnknownTemplate_EndToEnd(t *testing.T) {
	raw, err := os.ReadFile("../testdata/gribw-xcheck/complex_52_plain.grib2")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	patchAndValues := func(tmpl uint16) error {
		msg := bytes.Clone(raw)
		pos := 16
		for pos+5 <= len(msg) {
			slen := int(binary.BigEndian.Uint32(msg[pos : pos+4]))
			num := int(msg[pos+4])
			if num == 5 {
				binary.BigEndian.PutUint16(msg[pos+9:pos+11], tmpl)
				break
			}
			if slen < 5 {
				t.Fatalf("malformed fixture at offset %d", pos)
			}
			pos += slen
		}
		f, err := grib.OpenReader(bytes.NewReader(msg), int64(len(msg)))
		if err != nil {
			t.Fatalf("OpenReader: %v", err)
		}
		defer f.Close()
		m, err := f.Message(0)
		if err != nil {
			t.Fatalf("Message(0): %v", err)
		}
		_, err = m.Values()
		return err
	}

	t.Run("E8a template 5.10 (no spec def) -> *grib.ErrUnknownTemplate", func(t *testing.T) {
		err := patchAndValues(10)
		if err == nil {
			t.Fatal("Values() succeeded; want error for unregistered template 10")
		}
		var e *grib.ErrUnknownTemplate
		if !errors.As(err, &e) {
			t.Fatalf("want *grib.ErrUnknownTemplate, got %T: %v", err, err)
		}
		if e.Section != 5 || e.Number != 10 {
			t.Errorf("got {Section: %d, Number: %d}, want {Section: 5, Number: 10}", e.Section, e.Number)
		}
	})

	t.Run("E8b template 5.200 (spec def, unregistered) -> wrapped pack.ErrUnsupported", func(t *testing.T) {
		err := patchAndValues(200)
		if err == nil {
			t.Fatal("Values() succeeded; want error for unregistered template 200")
		}
		if !errors.Is(err, pack.ErrUnsupported) {
			t.Fatalf("want errors.Is(err, pack.ErrUnsupported), got %T: %v", err, err)
		}
		var e *grib.ErrUnknownTemplate
		if errors.As(err, &e) {
			t.Errorf("unexpected *grib.ErrUnknownTemplate for template with spec def: %+v", e)
		}
	})
}

// D5 (writer-side precision fail-loud) lives in gribw/xcheck_p6_b_gribw_test.go.
