// Package arch guarda testes de arquitetura: eles falham quando um pacote passa
// a importar algo que a direção das dependências não permite (ver
// docs/dependency-map.md). São baratos, rodam junto com a suíte normal e não
// precisam de rede.
package arch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pacotesExtraidos são os pacotes que nasceram do desmembramento do Agent
// (A-01). Eles prestam serviço ao orquestrador, nunca o contrário: importar
// agent de dentro deles fecharia um ciclo e travaria a extração do resto.
var pacotesExtraidos = []string{
	"agentic-harness/internal/present",
	"agentic-harness/internal/checkpoint",
	"agentic-harness/internal/memory",
	"agentic-harness/internal/evals",
}

// importsDe devolve os imports diretos do pacote. Um pacote que ainda não existe
// devolve nil: durante a refatoração em passos, o teste continua válido antes e
// depois de cada extração.
func importsDe(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", `{{join .Imports "\n"}}`, pkg)
	cmd.Dir = raizDoModulo(t)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var imps []string
	for _, linha := range strings.Split(string(out), "\n") {
		if linha = strings.TrimSpace(linha); linha != "" {
			imps = append(imps, linha)
		}
	}
	return imps
}

// raizDoModulo sobe até achar o go.mod, para que o go list rode com o mesmo
// módulo do teste (independente do diretório de trabalho do go test).
func raizDoModulo(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		pai := filepath.Dir(dir)
		if pai == dir {
			t.Fatal("go.mod não encontrado acima do diretório do teste")
		}
		dir = pai
	}
}

// TestPacotesExtraidosNaoImportamAgent garante o sentido da dependência: o
// Agent pode usar os pacotes extraídos, nunca o contrário.
func TestPacotesExtraidosNaoImportamAgent(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go não está no PATH")
	}
	for _, pkg := range pacotesExtraidos {
		imports := importsDe(t, pkg)
		for _, imp := range imports {
			if imp == "agentic-harness/internal/agent" {
				t.Errorf("%s importa internal/agent; a dependência deve apontar no sentido contrário", pkg)
			}
		}
	}
}
