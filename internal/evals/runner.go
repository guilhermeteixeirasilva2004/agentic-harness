package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Session é o agente sob teste, reduzido ao que uma avaliação precisa: rodar
// turnos, retomar do checkpoint e informar as perguntas feitas ao humano.
type Session interface {
	Turn(ctx context.Context, input string) (string, error)
	Continue(ctx context.Context) (string, error)
	Asked() []string
	Close() error
}

// Factory monta a sessão para uma tarefa. Recebe o diretório já preparado pelo
// Setup — é nele que o agente deve trabalhar (raiz confinada).
type Factory func(t Task, dir string) (Session, error)

// Options configura uma execução da linha de base.
type Options struct {
	Model string
	New   Factory
	// Timeout limita cada tarefa (0 = 3 minutos).
	Timeout time.Duration
	// Now injeta o relógio (testes).
	Now func() time.Time
}

// Result é uma linha do NDJSON da linha de base. Os campos são estáveis: o
// arquivo gerado é o "snapshot" comparado depois de cada refactor.
type Result struct {
	ID         string    `json:"id"`
	Task       string    `json:"tarefa"`
	Model      string    `json:"modelo"`
	At         time.Time `json:"data"`
	Criteria   string    `json:"criterio"`
	Answers    []string  `json:"respostas,omitempty"`
	Asked      []string  `json:"perguntas,omitempty"`
	Errors     []string  `json:"erros,omitempty"`
	Pass       bool      `json:"pass"`
	Obs        string    `json:"obs,omitempty"`
	DurationMS int64     `json:"duracao_ms"`
}

// Run executa as tarefas em ordem e devolve um resultado por tarefa. Falhas de
// montagem ou de execução não interrompem a suíte: entram em Errors e o grader
// decide o veredito com o que houver no diretório.
func Run(ctx context.Context, tasks []Task, opts Options) []Result {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	results := make([]Result, 0, len(tasks))
	for _, t := range tasks {
		results = append(results, runTask(ctx, t, opts, timeout, now))
	}
	return results
}

// runTask prepara o diretório, executa os turnos e aplica o grader.
func runTask(ctx context.Context, t Task, opts Options, timeout time.Duration, now func() time.Time) Result {
	inicio := time.Now()
	res := Result{
		ID:       t.ID,
		Task:     t.Prompt,
		Model:    opts.Model,
		At:       now(),
		Criteria: t.Criteria,
	}

	dir, err := os.MkdirTemp("", "eval-"+t.ID+"-")
	if err != nil {
		res.Errors = append(res.Errors, "preparação: "+err.Error())
		res.Pass = false
		res.Obs = "não foi possível criar o diretório da tarefa"
		res.DurationMS = time.Since(inicio).Milliseconds()
		return res
	}
	// Limpeza best-effort: o cenário é temporário e a falha ao remover não
	// invalida o resultado já apurado.
	defer func() { _ = os.RemoveAll(dir) }()

	if t.Setup != nil {
		if err := t.Setup(dir); err != nil {
			res.Errors = append(res.Errors, "setup: "+err.Error())
		}
	}

	var sessao Session
	if opts.New == nil {
		res.Errors = append(res.Errors, "runner sem fábrica de sessão")
	} else if sessao, err = opts.New(t, dir); err != nil {
		res.Errors = append(res.Errors, "montagem: "+err.Error())
	}

	if sessao != nil {
		runCtx, cancel := context.WithTimeout(ctx, timeout)
		for _, entrada := range t.Inputs() {
			out, err := sessao.Turn(runCtx, entrada)
			res.Answers = append(res.Answers, out)
			if err != nil {
				res.Errors = append(res.Errors, err.Error())
			}
		}
		if t.ContinueAfter {
			out, err := sessao.Continue(runCtx)
			res.Answers = append(res.Answers, out)
			if err != nil {
				res.Errors = append(res.Errors, "continue: "+err.Error())
			}
		}
		res.Asked = sessao.Asked()
		if err := sessao.Close(); err != nil {
			res.Errors = append(res.Errors, "encerramento: "+err.Error())
		}
		cancel()
	}

	if t.Grade != nil {
		g := t.Grade(GradeInput{
			Dir:     dir,
			Answers: res.Answers,
			Asked:   res.Asked,
			Errors:  res.Errors,
		})
		res.Pass, res.Obs = g.Pass, g.Obs
	} else {
		res.Obs = "tarefa sem grader"
	}
	res.DurationMS = time.Since(inicio).Milliseconds()
	return res
}

// WriteNDJSON grava um resultado por linha, no formato combinado da linha de
// base (id, tarefa, modelo, data, resultado, criterio, pass, obs, duracao_ms).
func WriteNDJSON(w io.Writer, results []Result) error {
	enc := json.NewEncoder(w)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("gravar resultado %s: %w", r.ID, err)
		}
	}
	return nil
}

// Select filtra o catálogo pelos IDs informados (vazio ou só espaços = todos).
func Select(tasks []Task, ids []string) ([]Task, error) {
	if len(ids) == 0 {
		return tasks, nil
	}
	porID := make(map[string]Task, len(tasks))
	for _, t := range tasks {
		porID[t.ID] = t
	}
	var out []Task
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		t, ok := porID[id]
		if !ok {
			return nil, fmt.Errorf("tarefa desconhecida: %q", id)
		}
		out = append(out, t)
	}
	return out, nil
}
