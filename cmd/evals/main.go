//go:build eval

// Command evals roda a linha de base de avaliação contra um modelo real
// (llama-server). Fica atrás da tag `eval` de propósito: o CI padrão não tem
// modelo e não deve depender de rede.
//
//	go run -tags=eval ./cmd/evals -url http://127.0.0.1:8080/v1 -model local
//	go run -tags=eval ./cmd/evals -list
//
// A saída é NDJSON (uma linha por tarefa), por padrão em
// docs/evals/baseline.ndjson. Ela é o "snapshot" do refactor: rode antes e
// depois de cada extração e compare o campo pass. (O caminho evita `evals/` na
// raiz porque `go build ./cmd/evals` criaria um binário homônimo.)
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentic-harness/internal/agent"
	"agentic-harness/internal/config"
	"agentic-harness/internal/evals"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
)

func main() {
	url := flag.String("url", envOu("HARNESS_URL", "http://127.0.0.1:8080/v1"), "base URL do servidor compatível com OpenAI")
	model := flag.String("model", envOu("HARNESS_MODEL", "local"), "modelo avaliado (vai no NDJSON)")
	saida := flag.String("out", filepath.Join("docs", "evals", "baseline.ndjson"), "arquivo NDJSON de saída")
	ids := flag.String("tasks", "", "IDs separados por vírgula (vazio = todas)")
	timeout := flag.Duration("timeout", 3*time.Minute, "limite por tarefa")
	temp := flag.Float64("temp", 0, "temperatura de amostragem")
	listar := flag.Bool("list", false, "apenas lista as tarefas e sai")
	flag.Parse()

	todas := evals.Tasks()
	if *listar {
		for _, t := range todas {
			fmt.Printf("%-22s %s\n", t.ID, t.Criteria)
		}
		return
	}

	selecionadas, err := evals.Select(todas, separarCSV(*ids))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[erro] %v\n", err)
		os.Exit(2)
	}
	// Mesma regra do CLI principal: http:// só para loopback.
	if err := llm.ValidateBaseURL(*url, false); err != nil {
		fmt.Fprintf(os.Stderr, "[erro] %v\n", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	inicio := time.Now()
	resultados := evals.Run(ctx, selecionadas, evals.Options{
		Model:   *model,
		Timeout: *timeout,
		New:     fabrica(*url, *model, *temp),
	})

	if err := gravar(*saida, resultados); err != nil {
		fmt.Fprintf(os.Stderr, "[erro] %v\n", err)
		os.Exit(2)
	}

	aprovadas := 0
	for _, r := range resultados {
		estado := "FALHOU"
		if r.Pass {
			estado = "ok"
			aprovadas++
		}
		fmt.Printf("[%-6s] %-22s %6d ms  %s\n", estado, r.ID, r.DurationMS, r.Obs)
	}
	fmt.Printf("\n%d/%d tarefas aprovadas em %s → %s\n",
		aprovadas, len(resultados), time.Since(inicio).Round(time.Millisecond), *saida)
}

// gravar escreve o NDJSON, criando o diretório de saída quando preciso.
func gravar(caminho string, resultados []evals.Result) error {
	if dir := filepath.Dir(caminho); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(caminho)
	if err != nil {
		return err
	}
	if err := evals.WriteNDJSON(f, resultados); err != nil {
		// O erro da escrita já está sendo devolvido; o Close é best-effort.
		_ = f.Close()
		return err
	}
	return f.Close()
}

// fabrica monta o agente de verdade para uma tarefa. O agente é o mesmo do
// subcomando "agent" (NewDefault): tools, aprovação e ask_user são os de
// produção; só a raiz e o aprovador mudam (headless e confinado ao cenário).
func fabrica(url, model string, temp float64) evals.Factory {
	return func(t evals.Task, dir string) (evals.Session, error) {
		cfg := &config.Config{
			BaseURL:     url,
			Model:       model,
			System:      "Você é um assistente útil, direto e conciso. Responda em português do Brasil.",
			Temperature: temp,
			Stream:      false,
			MaxSteps:    t.Steps(),
			Strategy:    "react",
			AllowTools:  t.Tools,
			ShellAllow:  t.ShellAllow,
		}
		// NewDefault confina as tools ao diretório atual: entrar no cenário
		// antes de montar e voltar ao terminar (a suíte é sequencial).
		anterior, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		if err := os.Chdir(dir); err != nil {
			return nil, err
		}
		a := agent.NewDefault(cfg)
		st, err := store.Open(filepath.Join(dir, ".eval-store.ndjson"))
		if err != nil {
			_ = os.Chdir(anterior)
			return nil, err
		}
		asker := &askerRoteirizado{respostas: append([]string(nil), t.AskAnswers...)}
		a.SetStore(st)
		a.SetSession("eval-" + t.ID)
		a.SetApprover(aprovadorHeadless{permitir: t.Aprova()})
		a.SetAsker(asker)
		return &sessao{a: a, asker: asker, store: st, anterior: anterior}, nil
	}
}

// sessao adapta o agente ao contrato do runner.
type sessao struct {
	a        *agent.Agent
	asker    *askerRoteirizado
	store    *store.Store
	anterior string
}

func (s *sessao) Turn(ctx context.Context, input string) (string, error) {
	return s.a.Turn(ctx, input, nil)
}

// Continue usa ResumeTask: com checkpoint pendente retoma do estado salvo; sem
// checkpoint, segue pelo histórico em memória.
func (s *sessao) Continue(ctx context.Context) (string, error) {
	return s.a.ResumeTask(ctx, nil)
}

func (s *sessao) Asked() []string { return s.asker.Perguntas() }

func (s *sessao) Close() error {
	err := s.store.Close()
	if s.anterior != "" {
		if chdirErr := os.Chdir(s.anterior); chdirErr != nil && err == nil {
			err = chdirErr
		}
	}
	return err
}

// aprovadorHeadless aplica o modo da tarefa sem terminal: auto aprova tudo,
// deny recusa tudo.
type aprovadorHeadless struct{ permitir bool }

func (a aprovadorHeadless) Approve(context.Context, agent.ApprovalRequest) (bool, error) {
	return a.permitir, nil
}

// askerRoteirizado devolve as respostas enfileiradas pela tarefa e grava as
// perguntas (o grader de ask_user depende disso).
type askerRoteirizado struct {
	mu        sync.Mutex
	respostas []string
	perguntas []string
}

func (a *askerRoteirizado) Ask(_ context.Context, pergunta string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.perguntas = append(a.perguntas, pergunta)
	if len(a.respostas) == 0 {
		return "", nil
	}
	resposta := a.respostas[0]
	a.respostas = a.respostas[1:]
	return resposta, nil
}

func (a *askerRoteirizado) Perguntas() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.perguntas...)
}

// envOu devolve a variável de ambiente ou o valor padrão.
func envOu(nome, padrao string) string {
	if v := strings.TrimSpace(os.Getenv(nome)); v != "" {
		return v
	}
	return padrao
}

// separarCSV aceita "a, b,c" e devolve os itens (o Select ignora espaços).
func separarCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, ",")
}
