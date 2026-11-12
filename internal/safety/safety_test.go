package safety

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNewDefaultsAndAllowlist(t *testing.T) {
	p := New(0, []string{"read_file", "", "  ", "get_time"})
	if p.MaxSteps != 4 {
		t.Fatalf("MaxSteps = %d; quero 4", p.MaxSteps)
	}
	if !p.AllowTool("read_file") || !p.AllowTool("get_time") {
		t.Fatalf("allowlist inesperada: %v", p.Allow)
	}
	if p.AllowTool("write_file") {
		t.Fatal("tool fora da allowlist foi liberada")
	}
	if err := p.CheckTool("write_file"); err == nil {
		t.Fatal("CheckTool deveria negar tool fora da allowlist")
	}
	if err := p.CheckTool("read_file"); err != nil {
		t.Fatalf("CheckTool(read_file) = %v", err)
	}
	if n := len(p.Allow); n != 2 {
		t.Fatalf("allowlist tem %d entradas; quero 2 (vazias devem ser ignoradas)", n)
	}
}

func TestCheckInput(t *testing.T) {
	p := New(2, nil)
	if err := p.CheckInput(strings.Repeat("a", p.MaxInputBytes)); err != nil {
		t.Fatalf("entrada no limite: %v", err)
	}
	err := p.CheckInput(strings.Repeat("a", p.MaxInputBytes+1))
	if err == nil || !strings.Contains(err.Error(), "excede o limite") {
		t.Fatalf("entrada acima do limite: err = %v", err)
	}
}

func TestTruncateOutput(t *testing.T) {
	p := New(1, nil)
	p.MaxOutputBytes = 64

	if got := p.TruncateOutput("curto"); got != "curto" {
		t.Fatalf("texto curto foi alterado: %q", got)
	}

	long := strings.Repeat("ção ", 100)
	got := p.TruncateOutput(long)
	if len(got) > p.MaxOutputBytes {
		t.Fatalf("saída tem %d bytes; teto é %d", len(got), p.MaxOutputBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("saída não é UTF-8 válido: %q", got)
	}
	if !strings.HasSuffix(got, "[... resposta truncada pela política]") {
		t.Fatalf("aviso de truncamento ausente: %q", got)
	}

	// Teto menor que o aviso: nada de pânico e corte ainda válido.
	p.MaxOutputBytes = 4
	got = p.TruncateOutput(long)
	if !utf8.ValidString(got) || len(got) > 4 {
		t.Fatalf("teto minúsculo: %q (%d bytes)", got, len(got))
	}
}

func TestStepsOK(t *testing.T) {
	p := New(3, nil)
	if !p.StepsOK(0) || !p.StepsOK(2) {
		t.Fatal("StepsOK negou passo dentro do teto")
	}
	if p.StepsOK(3) || p.StepsOK(4) {
		t.Fatal("StepsOK liberou passo acima do teto")
	}
}
