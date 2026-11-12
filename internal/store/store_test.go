package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRestringePermissoes(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sessoes")
	path := filepath.Join(sub, "sessions.jsonl")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	info, err := os.Stat(sub)
	if err != nil {
		t.Fatalf("Stat diretório: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("permissões do diretório = %o, esperava 700", perm)
	}
	info, err = os.Stat(path)
	if err != nil {
		t.Fatalf("Stat arquivo: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("permissões do arquivo = %o, esperava 600", perm)
	}
}

func TestAppendQuery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "sessions.jsonl")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	if err := s.Append("turn", "s1", map[string]any{"input": "oi", "output": "olá"}); err != nil {
		t.Fatalf("Append 1: %v", err)
	}
	if err := s.Append("turn", "s2", map[string]string{"input": "tchau"}); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	recs, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("registros = %d, esperava 2", len(recs))
	}
	if recs[0].Type != "turn" || recs[0].Session != "s1" {
		t.Fatalf("registro 0 = %+v", recs[0])
	}
	if !strings.Contains(string(recs[0].Payload), "oi") {
		t.Fatalf("payload = %s", recs[0].Payload)
	}
	if recs[0].At.IsZero() {
		t.Fatal("At deveria ser preenchido")
	}
}

func TestAppendPersisteEntreAberturas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")

	s1, err := Open(path)
	if err != nil {
		t.Fatalf("Open 1: %v", err)
	}
	if err := s1.Append("turn", "a", map[string]int{"n": 1}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open 2: %v", err)
	}
	defer s2.Close()
	if err := s2.Append("turn", "b", map[string]int{"n": 2}); err != nil {
		t.Fatalf("Append 2: %v", err)
	}

	recs, err := s2.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 2 || recs[0].Session != "a" || recs[1].Session != "b" {
		t.Fatalf("registros = %+v", recs)
	}
}

func TestQueryIgnoraLinhasCorrompidas(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	content := "{\"type\":\"turn\",\"session\":\"ok\",\"at\":\"2026-01-01T00:00:00Z\"}\n{lixo\n\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	recs, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 1 || recs[0].Session != "ok" {
		t.Fatalf("registros = %+v", recs)
	}
}

func TestQueryArquivoInexistente(t *testing.T) {
	s := &Store{path: filepath.Join(t.TempDir(), "nao-existe.jsonl")}
	recs, err := s.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("registros = %d, esperava 0", len(recs))
	}
}

func TestOpenCaminhoVazio(t *testing.T) {
	if _, err := Open("   "); err == nil {
		t.Fatal("Open com caminho vazio deveria falhar")
	}
}

func TestCloseIdempotente(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "s.jsonl"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close 1: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close 2: %v", err)
	}
	if err := s.Append("turn", "x", nil); err == nil {
		t.Fatal("Append após Close deveria falhar")
	}
}
