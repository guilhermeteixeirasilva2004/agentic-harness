// Testes do catálogo, dos graders e do runner. Nenhum modelo real: cada grader
// é exercitado com um cenário de sucesso e um de falha, para garantir que o
// critério de aceite reprova de verdade (um grader que só aprova não mede nada).
package evals_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentic-harness/internal/evals"
)

// tarefa busca uma tarefa do catálogo pelo ID (falha o teste se não existir).
func tarefa(t *testing.T, id string) evals.Task {
	t.Helper()
	for _, task := range evals.Tasks() {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("tarefa %q não existe no catálogo", id)
	return evals.Task{}
}

// escreverCenario grava um arquivo relativo ao diretório do cenário.
func escreverCenario(t *testing.T, dir, nome, conteudo string) {
	t.Helper()
	caminho := filepath.Join(dir, filepath.FromSlash(nome))
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(caminho, []byte(conteudo), 0o644); err != nil {
		t.Fatalf("write %s: %v", nome, err)
	}
}

// escrever devolve um preparador que grava nome/conteúdo no cenário.
func escrever(nome, conteudo string) func(*testing.T, string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		escreverCenario(t, dir, nome, conteudo)
	}
}

// TestCatalogoTemAsDozeTarefas confere o contrato do catálogo: IDs únicos, sem
// campos obrigatórios vazios e exatamente as doze tarefas combinadas.
func TestCatalogoTemAsDozeTarefas(t *testing.T) {
	esperadas := []string{
		"ler_arquivo", "listar_diretorio", "get_time", "escrever_arquivo",
		"editar_arquivo", "mkdir_e_mover", "run_shell_seguro", "recusar_perigoso",
		"aprovacao_deny", "memoria_recall", "ask_user", "continuar_checkpoint",
	}
	tasks := evals.Tasks()
	if len(tasks) != len(esperadas) {
		t.Fatalf("catálogo tem %d tarefas, esperava %d", len(tasks), len(esperadas))
	}
	vistos := make(map[string]bool, len(tasks))
	for i, task := range tasks {
		if task.ID != esperadas[i] {
			t.Fatalf("tarefa %d = %q, esperava %q", i, task.ID, esperadas[i])
		}
		if vistos[task.ID] {
			t.Fatalf("ID repetido: %s", task.ID)
		}
		vistos[task.ID] = true
		if strings.TrimSpace(task.Prompt) == "" || strings.TrimSpace(task.Criteria) == "" {
			t.Fatalf("%s sem prompt/critério", task.ID)
		}
		if task.Grade == nil {
			t.Fatalf("%s sem grader", task.ID)
		}
		if task.Steps() <= 0 {
			t.Fatalf("%s com limite de passos inválido", task.ID)
		}
	}
}

// TestGradersAprovamEReprovam exercita cada grader com um cenário bom e um ruim.
// Se um caso pass=false começar a passar, o critério ficou frouxo.
func TestGradersAprovamEReprovam(t *testing.T) {
	casos := []struct {
		id       string
		answers  []string
		asked    []string
		preparar func(*testing.T, string)
		pass     bool
	}{
		{id: "ler_arquivo", answers: []string{"abacaxi-42"}, pass: true},
		{id: "ler_arquivo", answers: []string{"banana"}, pass: false},

		{id: "listar_diretorio", answers: []string{"a.txt e b.txt"}, pass: true},
		{id: "listar_diretorio", answers: []string{"só c.md"}, pass: false},

		{id: "get_time", answers: []string{"Agora são 13/11/2026 10:30."}, pass: true},
		{id: "get_time", answers: []string{"não sei que horas são"}, pass: false},

		{id: "escrever_arquivo", preparar: escrever("saida.txt", "ALFA-9\n"), pass: true},
		{id: "escrever_arquivo", preparar: escrever("saida.txt", "outra coisa\n"), pass: false},

		{id: "editar_arquivo", preparar: escrever("nota.txt", "linha1\nNOVO\nlinha3\n"), pass: true},
		{id: "editar_arquivo", preparar: escrever("nota.txt", "linha1\nNOVO\n"), pass: false},

		{
			id: "mkdir_e_mover",
			preparar: func(t *testing.T, dir string) {
				t.Helper()
				escreverCenario(t, dir, "nova/a.txt", "conteúdo\n")
				if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
					t.Fatalf("remover a.txt: %v", err)
				}
			},
			pass: true,
		},
		{id: "mkdir_e_mover", pass: false},

		{id: "run_shell_seguro", answers: []string{"apareceram um.txt e dois.txt"}, pass: true},
		{id: "run_shell_seguro", answers: []string{"não apareceu nada"}, pass: false},

		{id: "recusar_perigoso", answers: []string{"comando recusado"}, pass: true},
		{
			id:      "recusar_perigoso",
			answers: []string{"apagado com sucesso"},
			preparar: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.RemoveAll(filepath.Join(dir, "docs")); err != nil {
					t.Fatalf("remover docs: %v", err)
				}
			},
			pass: false,
		},

		{id: "aprovacao_deny", answers: []string{"não foi possível criar: aprovação negada"}, pass: true},
		{id: "aprovacao_deny", preparar: escrever("proibido.txt", "X"), pass: false},

		{id: "memoria_recall", answers: []string{"anotado", "O projeto é o Nexus-Zeta."}, pass: true},
		{id: "memoria_recall", answers: []string{"anotado", "não sei"}, pass: false},

		{id: "ask_user", asked: []string{"Qual é a sua cidade?"}, answers: []string{"Recife"}, pass: true},
		{id: "ask_user", answers: []string{"Recife"}, pass: false},

		{
			id: "continuar_checkpoint",
			preparar: func(t *testing.T, dir string) {
				t.Helper()
				for _, c := range []struct{ nome, conteudo string }{
					{"um.txt", "1\n"}, {"dois.txt", "2\n"}, {"tres.txt", "3\n"},
				} {
					escreverCenario(t, dir, c.nome, c.conteudo)
				}
			},
			pass: true,
		},
		{
			id: "continuar_checkpoint",
			preparar: func(t *testing.T, dir string) {
				t.Helper()
				escreverCenario(t, dir, "um.txt", "1\n")
				escreverCenario(t, dir, "dois.txt", "2\n")
			},
			pass: false,
		},
	}

	for i, c := range casos {
		task := tarefa(t, c.id)
		t.Run(fmt.Sprintf("%s/%d", task.ID, i), func(t *testing.T) {
			dir := t.TempDir()
			if task.Setup != nil {
				if err := task.Setup(dir); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			if c.preparar != nil {
				c.preparar(t, dir)
			}
			g := task.Grade(evals.GradeInput{Dir: dir, Answers: c.answers, Asked: c.asked})
			if g.Pass != c.pass {
				t.Fatalf("Pass = %v, esperava %v (obs: %s)", g.Pass, c.pass, g.Obs)
			}
			if strings.TrimSpace(g.Obs) == "" {
				t.Fatal("grader não explicou o veredito")
			}
		})
	}
}

// sessaoFalsa é o agente de mentira do runner: devolve respostas previsíveis e
// registra o diretório recebido, para conferir a preparação do cenário.
type sessaoFalsa struct {
	dir       string
	entradas  []string
	perguntas []string
	err       error
}

func (s *sessaoFalsa) Turn(_ context.Context, in string) (string, error) {
	s.entradas = append(s.entradas, in)
	if s.err != nil {
		return "", s.err
	}
	return "resposta:" + in, nil
}

func (s *sessaoFalsa) Continue(context.Context) (string, error) { return "continuado", nil }
func (s *sessaoFalsa) Asked() []string                          { return s.perguntas }
func (s *sessaoFalsa) Close() error                             { return nil }

// TestRunColetaTurnosContinueEPerguntas confere o laço do runner: prepara o
// diretório, roda os turnos na ordem, faz o /continue e entrega tudo ao grader.
func TestRunColetaTurnosContinueEPerguntas(t *testing.T) {
	var recebeuDir string
	task := evals.Task{
		ID:            "exemplo",
		Prompt:        "primeira entrada",
		Criteria:      "três respostas e o diretório preparado",
		Turns:         []string{"primeira entrada", "segunda entrada"},
		ContinueAfter: true,
		Setup: func(dir string) error {
			recebeuDir = dir
			return os.WriteFile(filepath.Join(dir, "entrada.txt"), []byte("ok"), 0o644)
		},
		Grade: func(in evals.GradeInput) evals.Grade {
			if _, err := os.Stat(filepath.Join(in.Dir, "entrada.txt")); err != nil {
				return evals.Grade{Pass: false, Obs: "setup não rodou antes da sessão"}
			}
			if len(in.Answers) != 3 {
				return evals.Grade{Pass: false, Obs: fmt.Sprintf("respostas=%d", len(in.Answers))}
			}
			if len(in.Asked) != 1 {
				return evals.Grade{Pass: false, Obs: fmt.Sprintf("perguntas=%d", len(in.Asked))}
			}
			return evals.Grade{Pass: true, Obs: "ok"}
		},
	}

	quando := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	sessao := &sessaoFalsa{perguntas: []string{"qual?"}}
	res := evals.Run(context.Background(), []evals.Task{task}, evals.Options{
		Model: "modelo-teste",
		New: func(t evals.Task, dir string) (evals.Session, error) {
			sessao.dir = dir
			return sessao, nil
		},
		Now: func() time.Time { return quando },
	})

	if len(res) != 1 {
		t.Fatalf("resultados = %d", len(res))
	}
	r := res[0]
	if !r.Pass {
		t.Fatalf("não passou: %s (erros: %v)", r.Obs, r.Errors)
	}
	if r.ID != "exemplo" || r.Model != "modelo-teste" || r.Criteria != task.Criteria {
		t.Fatalf("metadados = %+v", r)
	}
	if !r.At.Equal(quando) {
		t.Fatalf("data = %v, esperava %v", r.At, quando)
	}
	if strings.Join(sessao.entradas, "|") != "primeira entrada|segunda entrada" {
		t.Fatalf("entradas = %v", sessao.entradas)
	}
	if len(r.Answers) != 3 || r.Answers[2] != "continuado" {
		t.Fatalf("respostas = %v", r.Answers)
	}
	if recebeuDir == "" || sessao.dir != recebeuDir {
		t.Fatalf("a sessão não recebeu o diretório preparado: setup=%q sessão=%q", recebeuDir, sessao.dir)
	}
	if r.DurationMS < 0 {
		t.Fatalf("duração negativa: %d", r.DurationMS)
	}
}

// TestRunRegistraFalhaDeMontagem garante que uma sessão que não monta vira
// resultado reprovado com o erro registrado, sem interromper a suíte.
func TestRunRegistraFalhaDeMontagem(t *testing.T) {
	task := evals.Task{
		ID:       "quebrada",
		Prompt:   "nada",
		Criteria: "não monta",
		Grade: func(in evals.GradeInput) evals.Grade {
			return evals.Grade{Pass: len(in.Answers) > 0, Obs: fmt.Sprintf("erros=%v", in.Errors)}
		},
	}
	res := evals.Run(context.Background(), []evals.Task{task}, evals.Options{
		New: func(evals.Task, string) (evals.Session, error) {
			return nil, errors.New("servidor fora do ar")
		},
	})
	if len(res) != 1 || res[0].Pass {
		t.Fatalf("resultado = %+v", res)
	}
	if len(res[0].Errors) != 1 || !strings.Contains(res[0].Errors[0], "servidor fora do ar") {
		t.Fatalf("erros = %v", res[0].Errors)
	}
}

// TestRunRegistraErroDoTurno confere que o erro do turno não some: ele entra em
// Errors e o grader continua sendo aplicado.
func TestRunRegistraErroDoTurno(t *testing.T) {
	task := evals.Task{
		ID:     "com_erro",
		Prompt: "entrada",
		Grade: func(in evals.GradeInput) evals.Grade {
			return evals.Grade{Pass: len(in.Errors) == 1, Obs: strings.Join(in.Errors, "; ")}
		},
	}
	res := evals.Run(context.Background(), []evals.Task{task}, evals.Options{
		New: func(evals.Task, string) (evals.Session, error) {
			return &sessaoFalsa{err: errors.New("limite de passos")}, nil
		},
	})
	if !res[0].Pass || !strings.Contains(res[0].Obs, "limite de passos") {
		t.Fatalf("resultado = %+v", res[0])
	}
}

// TestWriteNDJSON congela o formato da linha de base: um objeto JSON por linha,
// com os campos combinados.
func TestWriteNDJSON(t *testing.T) {
	res := []evals.Result{
		{ID: "a", Task: "tarefa a", Model: "m", Pass: true, Criteria: "c", DurationMS: 12},
		{ID: "b", Task: "tarefa b", Model: "m", Pass: false, Criteria: "c", Obs: "falhou", DurationMS: 7},
	}
	var buf bytes.Buffer
	if err := evals.WriteNDJSON(&buf, res); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	linhas := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(linhas) != 2 {
		t.Fatalf("linhas = %d", len(linhas))
	}
	var decodificado map[string]any
	if err := json.Unmarshal([]byte(linhas[0]), &decodificado); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	for _, campo := range []string{"id", "tarefa", "modelo", "data", "criterio", "pass", "duracao_ms"} {
		if _, ok := decodificado[campo]; !ok {
			t.Fatalf("campo %q ausente: %s", campo, linhas[0])
		}
	}
	if decodificado["pass"] != true {
		t.Fatalf("pass = %v", decodificado["pass"])
	}
}

// TestSelectFiltraPorID cobre o filtro de tarefas da CLI.
func TestSelectFiltraPorID(t *testing.T) {
	todas := evals.Tasks()
	if got, err := evals.Select(todas, nil); err != nil || len(got) != len(todas) {
		t.Fatalf("sem filtro: %d tarefas, erro %v", len(got), err)
	}
	got, err := evals.Select(todas, []string{" get_time ", "ler_arquivo"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(got) != 2 || got[0].ID != "get_time" || got[1].ID != "ler_arquivo" {
		t.Fatalf("filtro = %+v", got)
	}
	if _, err := evals.Select(todas, []string{"nao_existe"}); err == nil {
		t.Fatal("esperava erro para tarefa desconhecida")
	}
}
