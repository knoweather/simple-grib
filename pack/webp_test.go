package pack

import (
	"errors"
	"strings"
	"testing"

	"github.com/knoweather/simple-grib/spec"
)

func TestWebPTemplateRegistration(t *testing.T) {
	def, ok := spec.Template(5, TemplateWebP)
	if !ok {
		t.Fatal("template 5.49152 not registered")
	}
	if def.Name != "WebP image (local use)" {
		t.Errorf("name = %q, want %q", def.Name, "WebP image (local use)")
	}
	if len(def.Fields) != 0 {
		t.Errorf("fields = %d, want 0: the payload is an image, not packing fields", len(def.Fields))
	}
}

func TestWebPUnpackRefusesNumericDecoding(t *testing.T) {
	_, err := Unpack(&Context{
		TemplateNumber: TemplateWebP,
		Data:           []byte("RIFF\x00\x00\x00\x00WEBP"),
	})
	var e *ErrNotNumeric
	if !errors.As(err, &e) {
		t.Fatalf("err = %v (%T), want *ErrNotNumeric", err, err)
	}
	if e.Template != TemplateWebP {
		t.Errorf("Template = %d, want %d", e.Template, TemplateWebP)
	}
	if !strings.Contains(err.Error(), "Message.WebP") {
		t.Errorf("error %q should point at Message.WebP", err)
	}
}
