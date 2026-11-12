package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCall(t *testing.T) {
	cases := []struct {
		name string
		in   string
		tool string
	}{
		{"objeto puro", `{"tool":"get_time","arguments":{}}`, "get_time"},
		{"cerca json", "```json\n{\"tool\":\"read_file\",\"arguments\":{\"path\":\"a.txt\"}}\n```", "read_file"},
		{"texto ao redor", `vou chamar: {"tool":"get_time","arguments":{}} agora`, "get_time"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			call, err := ParseCall(tc.in)
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if call.Tool != tc.tool {
				t.Fatalf("tool = %q, esperava %q", call.Tool, tc.tool)
			}
		})
	}
}

func TestParseCallSemTool(t *testing.T) {
	if _, err := ParseCall(`{"arguments":{}}`); err == nil {
		t.Fatal("esperava erro para chamada sem nome de tool")
	}
	if _, err := ParseCall("texto qualquer"); err == nil {
		t.Fatal("esperava erro para saída sem objeto JSON")
	}
}

func TestExtractObjectComChavesEmString(t *testing.T) {
	obj, ok := ExtractObject(`lixo {"answer":"a } b","action":"final"} lixo`)
	if !ok {
		t.Fatal("esperava encontrar objeto")
	}
	if want := `{"answer":"a } b","action":"final"}`; obj != want {
		t.Fatalf("objeto = %q, esperava %q", obj, want)
	}
}

func TestRegistryDispatch(t *testing.T) {
	r := NewRegistry()
	r.Add(GetTime{})
	r.Allow("get_time")

	out, err := r.Dispatch(context.Background(), Call{Tool: "get_time", Arguments: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out == "" {
		t.Fatal("saída vazia")
	}
}

func TestRegistryBloqueiaForaDaAllowlist(t *testing.T) {
	r := NewRegistry()
	r.Add(GetTime{})
	r.Allow()

	_, err := r.Dispatch(context.Background(), Call{Tool: "get_time"})
	if !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("erro = %v, esperava ErrNotAllowed", err)
	}
}

func TestRegistryDesconhecida(t *testing.T) {
	r := NewRegistry()
	r.Allow("get_time")
	if _, err := r.Dispatch(context.Background(), Call{Tool: "get_time"}); err == nil {
		t.Fatal("esperava erro para tool desconhecida")
	}
}

func TestCatalogListaApenasPermitidas(t *testing.T) {
	r := NewRegistry()
	r.Add(GetTime{})
	r.Add(ReadFile{Root: t.TempDir()})
	r.Allow("read_file")

	cat := r.Catalog()
	if !strings.Contains(cat, "read_file") || strings.Contains(cat, "get_time") {
		t.Fatalf("catálogo inesperado: %q", cat)
	}
}

func TestReadFileRespeitaRaiz(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("conteudo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ReadFile{Root: dir}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "conteudo" {
		t.Fatalf("conteúdo = %q", out)
	}

	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"../fora.txt"}`)); err == nil {
		t.Fatal("esperava erro para caminho fora da raiz")
	}
}

func TestReadFileExigePath(t *testing.T) {
	tool := ReadFile{Root: t.TempDir()}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("esperava erro para path ausente")
	}
}

func TestReadFileTrunca(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := ReadFile{Root: dir, MaxBytes: 4}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"b.txt"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.HasPrefix(out, "0123") || !strings.Contains(out, "truncado") {
		t.Fatalf("saída = %q", out)
	}
}

func TestValidateArgsObrigatorios(t *testing.T) {
	tool := ReadFile{Root: t.TempDir()}

	err := ValidateArgs(tool, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "path") {
		t.Fatalf("erro = %v, esperava menção a path", err)
	}
	if err := ValidateArgs(tool, json.RawMessage(`{"path":"a.txt"}`)); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if err := ValidateArgs(GetTime{}, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("get_time não exige argumentos: %v", err)
	}
}
