// Testes de integração do runner de evals com o agente de verdade e o FakeLLM:
// provam que o harness (setup → turnos → aprovação → grader) funciona sem
// modelo real. As tarefas que dependem de remember/ask_user ficam para o
// cmd/evals, que monta o NewDefault completo.
package agent_test

import (
	"context"
	"testing"

	"agentic-harness/internal/agent"
	"agentic-harness/internal/config"
	"agentic-harness/internal/evals"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/testutil"
	"agentic-harness/internal/tools"
)

// sessaoAgente adapta o agente ao contrato do runner de evals.
type sessaoAgente struct{ a *agent.Agent }

func (s sessaoAgente) Turn(ctx context.Context, input string) (string, error) {
	return s.a.Turn(ctx, input, nil)
}

func (s sessaoAgente) Continue(ctx context.Context) (string, error) {
	return s.a.ResumeTask(ctx, nil)
}

func (s sessaoAgente) Asked() []string { return nil }
func (s sessaoAgente) Close() error    { return nil }

// rodarEval executa uma tarefa do catálogo com o agente real (tools confinadas
// ao diretório do cenário, aprovador conforme a tarefa).
func rodarEval(t *testing.T, f *testutil.FakeLLM, id string) evals.Result {
	t.Helper()
	tasks, err := evals.Select(evals.Tasks(), []string{id})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	res := evals.Run(context.Background(), tasks, evals.Options{
		Model: "fake",
		New: func(task evals.Task, dir string) (evals.Session, error) {
			cfg := &config.Config{System: "teste", MaxSteps: task.Steps(), AllowTools: task.Tools}
			reg := tools.NewRegistry()
			reg.Add(tools.GetTime{})
			reg.Add(tools.ReadFile{Root: dir})
			reg.Add(tools.WriteFile{Root: dir})
			reg.Allow(task.Tools...)
			a := agent.New(cfg, f, reg, safety.New(cfg.MaxSteps, cfg.AllowTools))
			a.SetApprover(testutil.NewApprover[agent.ApprovalRequest](task.Aprova()))
			return sessaoAgente{a: a}, nil
		},
	})
	if len(res) != 1 {
		t.Fatalf("resultados = %d", len(res))
	}
	return res[0]
}

// TestEvalLerArquivoAprovaComAgenteReal cobre o caminho completo de leitura.
func TestEvalLerArquivoAprovaComAgenteReal(t *testing.T) {
	f := testutil.NewFakeLLM(
		testutil.Text(`{"action":"tool","tool":"read_file","arguments":{"path":"dados.txt"}}`),
		testutil.Text(`{"action":"final","answer":"abacaxi-42"}`),
	)
	r := rodarEval(t, f, "ler_arquivo")
	if !r.Pass {
		t.Fatalf("não passou: %s (erros: %v)", r.Obs, r.Errors)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("erros inesperados: %v", r.Errors)
	}
}

// TestEvalLerArquivoReprovaRespostaErrada garante que o grader não é carimbo:
// a mesma tarefa, com resposta errada, reprova.
func TestEvalLerArquivoReprovaRespostaErrada(t *testing.T) {
	f := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"banana"}`))
	r := rodarEval(t, f, "ler_arquivo")
	if r.Pass {
		t.Fatalf("passou com resposta errada: %s", r.Obs)
	}
}

// TestEvalEscreverArquivoAprovaComToolMutante cobre tool mutante + aprovação
// automática + verificação do efeito no disco.
func TestEvalEscreverArquivoAprovaComToolMutante(t *testing.T) {
	f := testutil.NewFakeLLM(
		testutil.Text(`{"action":"tool","tool":"write_file","arguments":{"path":"saida.txt","content":"ALFA-9"}}`),
		testutil.Text(`{"action":"final","answer":"arquivo criado"}`),
	)
	r := rodarEval(t, f, "escrever_arquivo")
	if !r.Pass {
		t.Fatalf("não passou: %s (erros: %v)", r.Obs, r.Errors)
	}
}
