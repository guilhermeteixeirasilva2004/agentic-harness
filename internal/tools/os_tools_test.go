package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequiresApproval(t *testing.T) {
	for _, tc := range []struct {
		tool Tool
		want bool
	}{
		{GetTime{}, false},
		{ReadFile{Root: t.TempDir()}, false},
		{WriteFile{Root: t.TempDir()}, true},
		{EditFile{Root: t.TempDir()}, true},
		{DeleteFile{Root: t.TempDir()}, true},
		{MoveFile{Root: t.TempDir()}, true},
		{Mkdir{Root: t.TempDir()}, true},
		{RunShell{Root: t.TempDir()}, true},
	} {
		if got := RequiresApproval(tc.tool); got != tc.want {
			t.Fatalf("%s: RequiresApproval = %v, esperava %v", tc.tool.Name(), got, tc.want)
		}
	}
}

func TestWriteFileCriaArquivo(t *testing.T) {
	dir := t.TempDir()
	tool := WriteFile{Root: dir}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"sub/a.txt","content":"oi"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("saída = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sub", "a.txt"))
	if err != nil {
		t.Fatalf("arquivo não criado: %v", err)
	}
	if string(data) != "oi" {
		t.Fatalf("conteúdo = %q", data)
	}
}

func TestWriteFileNaoSobrescreveSemOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("antigo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := WriteFile{Root: dir}

	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","content":"novo"}`)); err == nil {
		t.Fatal("esperava erro ao sobrescrever sem overwrite")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","content":"novo","overwrite":true}`)); err != nil {
		t.Fatalf("overwrite deveria funcionar: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "novo" {
		t.Fatalf("conteúdo = %q", data)
	}
}

func TestWriteFileRecusaForaDaRaiz(t *testing.T) {
	tool := WriteFile{Root: t.TempDir()}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"../fora.txt","content":"x"}`)); err == nil {
		t.Fatal("esperava erro para caminho fora da raiz")
	}
}

func TestEditFileSubstituiUmaVez(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("alfa beta"), 0o640); err != nil {
		t.Fatal(err)
	}
	tool := EditFile{Root: dir}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","old":"beta","new":"gama"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "editado") {
		t.Fatalf("saída = %q", out)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "alfa gama" {
		t.Fatalf("conteúdo = %q", data)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("permissões mudaram: %v", info.Mode().Perm())
	}
}

func TestEditFileErros(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("repete repete"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := EditFile{Root: dir}

	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","old":"ausente","new":"x"}`)); err == nil {
		t.Fatal("esperava erro para trecho ausente")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","old":"repete","new":"x"}`)); err == nil {
		t.Fatal("esperava erro para trecho ambíguo")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","old":"","new":"x"}`)); err == nil {
		t.Fatal("esperava erro para old vazio")
	}
}

func TestRunShellPermitido(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := RunShell{Root: dir, Allow: []string{"ls"}}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"command":"ls"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Fatalf("saída = %q", out)
	}
}

func TestRunShellBloqueiaPrefixo(t *testing.T) {
	tool := RunShell{Root: t.TempDir(), Allow: []string{"ls"}}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"command":"rm -rf x"}`)); err == nil {
		t.Fatal("esperava erro para comando fora da allowlist")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"command":"ls -la"}`)); err != nil {
		t.Fatalf("ls -la deveria ser aceito pelo prefixo: %v", err)
	}
}

func TestRunShellRecusaMetacaracteres(t *testing.T) {
	for _, cmd := range []string{"ls; rm -rf .", "ls | wc -l", "ls > out.txt", "echo $(whoami)"} {
		if err := CheckCommand(cmd, []string{"ls", "echo"}); err == nil {
			t.Fatalf("esperava bloqueio para %q", cmd)
		}
	}
}

func TestRunShellTimeout(t *testing.T) {
	tool := RunShell{Root: t.TempDir(), Allow: []string{"sleep"}, Timeout: 30 * time.Millisecond}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"command":"sleep 2"}`)); err == nil {
		t.Fatal("esperava erro de timeout")
	}
}

func TestRunShellLimitaSaida(t *testing.T) {
	tool := RunShell{Root: t.TempDir(), Allow: []string{"echo"}, MaxBytes: 8}
	out, err := tool.Run(context.Background(), json.RawMessage(`{"command":"echo 0123456789abcdef"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "truncada") {
		t.Fatalf("saída = %q, esperava marca de truncamento", out)
	}
}

func TestRunShellCwdForaDaRaiz(t *testing.T) {
	tool := RunShell{Root: t.TempDir(), Allow: []string{"ls"}}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"command":"ls","cwd":".."}`)); err == nil {
		t.Fatal("esperava erro para cwd fora da raiz")
	}
}

func TestResolveWithinSymlink(t *testing.T) {
	outside := t.TempDir()
	root := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink indisponível: %v", err)
	}
	tool := ReadFile{Root: root}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"link/segredo.txt"}`)); err == nil {
		t.Fatal("esperava erro ao atravessar symlink para fora da raiz")
	}
}

func TestDeleteFileRemoveArquivo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := DeleteFile{Root: dir}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "removido") {
		t.Fatalf("saída = %q", out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("arquivo ainda existe: %v", err)
	}
}

func TestDeleteFileDiretorioExigeRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "build")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := DeleteFile{Root: dir}

	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"build"}`)); err == nil {
		t.Fatal("esperava erro ao remover diretório sem recursive")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"build","recursive":true}`)); err != nil {
		t.Fatalf("recursive deveria funcionar: %v", err)
	}
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Fatalf("diretório ainda existe: %v", err)
	}
}

func TestDeleteFileRecusaForaDaRaizERaiz(t *testing.T) {
	tool := DeleteFile{Root: t.TempDir()}

	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"../fora.txt"}`)); err == nil {
		t.Fatal("esperava erro para caminho fora da raiz")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"."}`)); err == nil {
		t.Fatal("esperava erro ao remover a raiz do projeto")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"ausente.txt"}`)); err == nil {
		t.Fatal("esperava erro para arquivo inexistente")
	}
}

func TestMoveFileRenomeia(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("oi"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := MoveFile{Root: dir}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","to":"sub/b.txt"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "movido") {
		t.Fatalf("saída = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("origem ainda existe: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "sub", "b.txt"))
	if err != nil || string(data) != "oi" {
		t.Fatalf("destino inválido: %q, %v", data, err)
	}
}

func TestMoveFileNaoSobrescreveSemOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("antigo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("novo"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := MoveFile{Root: dir}

	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","to":"b.txt"}`)); err == nil {
		t.Fatal("esperava erro ao sobrescrever sem overwrite")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a.txt","to":"b.txt","overwrite":true}`)); err != nil {
		t.Fatalf("overwrite deveria funcionar: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "b.txt"))
	if string(data) != "antigo" {
		t.Fatalf("conteúdo = %q", data)
	}
}

func TestMoveFileErros(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pasta", "filha"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := MoveFile{Root: dir}

	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"pasta","to":"pasta/filha"}`)); err == nil {
		t.Fatal("esperava erro ao mover diretório para dentro de si")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"nao-existe","to":"x"}`)); err == nil {
		t.Fatal("esperava erro para origem inexistente")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"pasta","to":"../fora"}`)); err == nil {
		t.Fatal("esperava erro para destino fora da raiz")
	}
}

func TestMkdirCriaDiretorios(t *testing.T) {
	dir := t.TempDir()
	tool := Mkdir{Root: dir}

	out, err := tool.Run(context.Background(), json.RawMessage(`{"path":"a/b/c"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "diretório criado") {
		t.Fatalf("saída = %q", out)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "b", "c")); err != nil || !info.IsDir() {
		t.Fatalf("diretório não criado: %v", err)
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":"../fora"}`)); err == nil {
		t.Fatal("esperava erro para caminho fora da raiz")
	}
	if _, err := tool.Run(context.Background(), json.RawMessage(`{"path":""}`)); err == nil {
		t.Fatal("esperava erro para path vazio")
	}
}
