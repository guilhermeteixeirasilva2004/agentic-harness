package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// B-01: o corte por bytes não pode partir uma runa acentuada ao meio.
func TestReadFileTruncationKeepsUTF8(t *testing.T) {
	dir := t.TempDir()
	content := "ação ação ação"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// 2 bytes caem no meio do "ç" (a=1, ç=2, ã=2, ...).
	args := json.RawMessage(`{"path":"a.txt","max_bytes":2}`)
	out, err := (ReadFile{Root: dir}).Run(context.Background(), args)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.HasPrefix(out, "a\n") {
		t.Fatalf("saída = %q; quero o prefixo \"a\" seguido do aviso", out)
	}
	if !strings.Contains(out, "truncado em 2 bytes") {
		t.Fatalf("aviso de truncamento ausente: %q", out)
	}
	body := strings.SplitN(out, "\n", 2)[0]
	if !utf8.ValidString(body) {
		t.Fatalf("corpo não é UTF-8 válido: %q", body)
	}
	if !strings.HasPrefix(content, body) {
		t.Fatalf("corpo %q não é prefixo de %q", body, content)
	}
}

func TestLimitedBufferKeepsUTF8(t *testing.T) {
	buf := &limitedBuffer{limit: 4}
	// "ação" tem 6 bytes; o limite de 4 cai no meio do "ã".
	if _, err := buf.Write([]byte("ação")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "aç") {
		t.Fatalf("saída = %q; quero prefixo \"aç\"", got)
	}
	body := strings.SplitN(got, "\n", 2)[0]
	if !utf8.ValidString(body) {
		t.Fatalf("corpo não é UTF-8 válido: %q", body)
	}
	if !strings.Contains(got, "saída truncada em 4 bytes") {
		t.Fatalf("aviso de truncamento ausente: %q", got)
	}

	// Sem truncamento, o texto passa inteiro.
	buf2 := &limitedBuffer{limit: 64}
	if _, err := buf2.Write([]byte("ação")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf2.String(); got != "ação" {
		t.Fatalf("saída = %q; quero \"ação\"", got)
	}
	// Escritas depois do limite são descartadas sem erro.
	if n, err := buf2.Write([]byte("x")); err != nil || n != 1 {
		t.Fatalf("Write pós-limite = %d, %v", n, err)
	}
}
