package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{name: "cabe", in: "ação", limit: 10, want: "ação"},
		{name: "exato em runas", in: "ação", limit: 4, want: "ação"},
		{name: "corta runas acentuadas", in: "ação", limit: 2, want: "aç…"},
		{name: "limite zero", in: "ação", limit: 0, want: ""},
		{name: "negativo", in: "ação", limit: -3, want: ""},
		{name: "emoji conta uma runa", in: "a😀b", limit: 2, want: "a😀…"},
		{name: "vazio", in: "", limit: 3, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TruncateRunes(tc.in, tc.limit); got != tc.want {
				t.Fatalf("TruncateRunes(%q, %d) = %q; quero %q", tc.in, tc.limit, got, tc.want)
			}
		})
	}
}

func TestCutBytesUTF8NeverSplitsRunes(t *testing.T) {
	in := "ação" // a(1) ç(2) ã(2) o(1)
	for limit := 0; limit <= len(in)+2; limit++ {
		out := CutBytesUTF8(in, limit)
		if !utf8.ValidString(out) {
			t.Fatalf("limit=%d produziu UTF-8 inválido: %q", limit, out)
		}
		if len(out) > limit && limit > 0 {
			t.Fatalf("limit=%d devolveu %d bytes", limit, len(out))
		}
		if !strings.HasPrefix(in, out) {
			t.Fatalf("limit=%d não é prefixo de %q: %q", limit, in, out)
		}
	}
	if got := CutBytesUTF8(in, 1); got != "a" {
		t.Fatalf("LimitBytesUTF8(%q, 1) = %q; quero %q", in, got, "a")
	}
	// limite cai no meio do "ç": recua para "a".
	if got := CutBytesUTF8(in, 2); got != "a" {
		t.Fatalf("LimitBytesUTF8(%q, 2) = %q; quero %q", in, got, "a")
	}
	if got := CutBytesUTF8(in, 3); got != "aç" {
		t.Fatalf("LimitBytesUTF8(%q, 3) = %q; quero %q", in, got, "aç")
	}
}

func TestTruncateBytesUTF8(t *testing.T) {
	in := "ação"
	if got := TruncateBytesUTF8(in, len(in)); got != in {
		t.Fatalf("sem corte: %q", got)
	}
	if got := TruncateBytesUTF8(in, 2); got != "a"+Ellipsis {
		t.Fatalf("TruncateBytes = %q; quero %q", got, "a"+Ellipsis)
	}
	if got := TruncateBytesUTF8(in, 0); got != "" {
		t.Fatalf("limite zero = %q; quero vazio", got)
	}
	// Bytes inválidos não devem travar nem gerar pânico.
	if got := CutBytesUTF8("\xff\xfe\xfd", 2); len(got) > 2 {
		t.Fatalf("bytes inválidos: %q", got)
	}
}
