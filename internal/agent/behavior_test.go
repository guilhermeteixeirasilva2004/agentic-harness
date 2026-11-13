// Testes de caracterização do comportamento ATUAL do agente, escritos pela API
// pública (pacote agent_test): nenhum campo privado é acessado. Eles existem
// para que a refatoração (A-01/A-03/A-04) possa ser verificada passo a passo —
// inclusive os comportamentos "estranhos" que não devem mudar sem decisão
// explícita.
package agent_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentic-harness/internal/agent"
	"agentic-harness/internal/config"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/store"
	"agentic-harness/internal/testutil"
	"agentic-harness/internal/tools"
)

// novaCenario monta um agente real com o FakeLLM e o registry confinado a um
// diretório temporário. A allowlist vem de cfg.AllowTools, como em produção.
func novaCenario(t *testing.T, f *testutil.FakeLLM, cfg *config.Config) (*agent.Agent, string) {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 4
	}
	if cfg.System == "" {
		cfg.System = "teste"
	}
	dir := t.TempDir()
	reg := tools.NewRegistry()
	reg.Add(tools.GetTime{})
	reg.Add(tools.ReadFile{Root: dir})
	reg.Add(tools.WriteFile{Root: dir})
	reg.Allow(cfg.AllowTools...)
	return agent.New(cfg, f, reg, safety.New(cfg.MaxSteps, cfg.AllowTools)), dir
}

// comStore conecta o agente a um arquivo NDJSON de sessões no diretório do
// cenário; o arquivo é fechado no fim do teste.
func comStore(t *testing.T, a *agent.Agent, dir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "sessoes.ndjson"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	a.SetStore(st)
}

// TestBehaviorRespostaForaDoContratoViraRespostaFinal caracteriza o fallback de
// parseDecision: texto que não é JSON de decisão não vira erro, vira a resposta
// final (o turno não trava).
func TestBehaviorRespostaForaDoContratoViraRespostaFinal(t *testing.T) {
	f := testutil.NewFakeLLM(testutil.Text("não é um JSON de decisão"))
	a, _ := novaCenario(t, f, nil)

	out, err := a.Turn(context.Background(), "oi", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "não é um JSON de decisão" {
		t.Fatalf("resposta = %q", out)
	}
	want := "[user] oi\n[assistant] não é um JSON de decisão\n"
	if got := a.History(); got != want {
		t.Fatalf("histórico = %q, esperava %q", got, want)
	}
}

// TestBehaviorFalhaAoMontarArgumentosNaoExecutaTool caracteriza o B-03: quando
// o fillArgs falha, a tool NÃO roda com argumentos vazios; o modelo recebe
// "ERRO (args)" e pode se recuperar no passo seguinte.
func TestBehaviorFalhaAoMontarArgumentosNaoExecutaTool(t *testing.T) {
	cfg := &config.Config{MaxSteps: 4, AllowTools: []string{"write_file"}}
	f := testutil.NewFakeLLM(
		testutil.Text(`{"action":"tool","tool":"write_file"}`),
		testutil.Text("isto não é o objeto de argumentos"),
		testutil.Text(`{"action":"final","answer":"ok"}`),
	)
	a, dir := novaCenario(t, f, cfg)
	a.SetApprover(testutil.NewApprover[agent.ApprovalRequest](true))

	out, err := a.Turn(context.Background(), "crie o arquivo", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("resposta = %q", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "x.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("a tool executou mesmo sem argumentos (stat: %v)", statErr)
	}
	if h := a.History(); !strings.Contains(h, "ERRO (args)") {
		t.Fatalf("histórico não contém o erro de argumentos: %q", h)
	}
}

// TestBehaviorAprovacaoNegadaNaoExecutaTool caracteriza o gate de aprovação:
// tanto uma negativa quanto um erro do Approver impedem a execução e devolvem
// "APROVAÇÃO NEGADA" ao modelo (fail-closed).
func TestBehaviorAprovacaoNegadaNaoExecutaTool(t *testing.T) {
	casos := map[string]testutil.ApproverFunc[agent.ApprovalRequest]{
		"negada": func(context.Context, agent.ApprovalRequest) (bool, error) {
			return false, nil
		},
		"erro na aprovação": func(context.Context, agent.ApprovalRequest) (bool, error) {
			return false, errors.New("sem terminal interativo")
		},
	}
	for nome, aprovar := range casos {
		t.Run(nome, func(t *testing.T) {
			cfg := &config.Config{MaxSteps: 4, AllowTools: []string{"write_file"}}
			f := testutil.NewFakeLLM(
				testutil.Text(`{"action":"tool","tool":"write_file","arguments":{"path":"x.txt","content":"x"}}`),
				testutil.Text(`{"action":"final","answer":"ok"}`),
			)
			a, dir := novaCenario(t, f, cfg)
			a.SetApprover(aprovar)

			out, err := a.Turn(context.Background(), "crie x.txt", nil)
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if out != "ok" {
				t.Fatalf("resposta = %q", out)
			}
			if _, statErr := os.Stat(filepath.Join(dir, "x.txt")); !os.IsNotExist(statErr) {
				t.Fatal("a tool mutante executou apesar da negativa")
			}
			if h := a.History(); !strings.Contains(h, "APROVAÇÃO NEGADA (write_file)") {
				t.Fatalf("histórico não contém a negativa: %q", h)
			}
		})
	}
}

// TestBehaviorToolForaDaAllowlistNaoExecuta caracteriza o "negar por padrão":
// a política barra a tool mesmo que o modelo a peça, e o erro volta como
// observação.
func TestBehaviorToolForaDaAllowlistNaoExecuta(t *testing.T) {
	f := testutil.NewFakeLLM(
		testutil.Text(`{"action":"tool","tool":"get_time"}`),
		testutil.Text(`{"action":"final","answer":"sem permissão"}`),
	)
	a, _ := novaCenario(t, f, &config.Config{MaxSteps: 3}) // nenhuma tool liberada

	out, err := a.Turn(context.Background(), "que horas são?", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "sem permissão" {
		t.Fatalf("resposta = %q", out)
	}
	if h := a.History(); !strings.Contains(h, "tool bloqueada pela política: get_time") {
		t.Fatalf("histórico não registra o bloqueio: %q", h)
	}
}

// turnosDeLeitura devolve duas decisões válidas de read_file (com argumentos,
// para não passar pelo fillArgs). O arquivo não existe: a tool devolve ERRO e o
// loop consome os passos, que é o que queremos caracterizar.
func turnosDeLeitura() []testutil.Reply {
	decisao := testutil.Text(`{"action":"tool","tool":"read_file","arguments":{"path":"naoexiste.txt"}}`)
	return []testutil.Reply{decisao, decisao}
}

// TestBehaviorPassosEsgotadosGravaCheckpoint caracteriza o fim do turno sem
// resposta final: grava checkpoint e devolve erro que instrui o /continue.
func TestBehaviorPassosEsgotadosGravaCheckpoint(t *testing.T) {
	cfg := &config.Config{MaxSteps: 2, AllowTools: []string{"read_file"}}
	f := testutil.NewFakeLLM(turnosDeLeitura()...)
	a, dir := novaCenario(t, f, cfg)
	comStore(t, a, dir)

	_, err := a.Turn(context.Background(), "tarefa longa", nil)
	if err == nil {
		t.Fatal("esperava erro de limite de passos")
	}
	if !strings.Contains(err.Error(), "limite de 2 passos") || !strings.Contains(err.Error(), "/continue") {
		t.Fatalf("erro = %q", err)
	}
	if !a.HasCheckpoint() {
		t.Fatal("checkpoint não foi gravado")
	}
}

// TestBehaviorContinueRestauraEstadoDoCheckpoint caracteriza a retomada: o
// histórico anterior (com observações) volta, o prompt de continuação é
// enviado, e um turno bem-sucedido consome o checkpoint.
func TestBehaviorContinueRestauraEstadoDoCheckpoint(t *testing.T) {
	cfg := &config.Config{MaxSteps: 2, AllowTools: []string{"read_file"}}
	f := testutil.NewFakeLLM(turnosDeLeitura()...)
	a, dir := novaCenario(t, f, cfg)
	comStore(t, a, dir)

	if _, err := a.Turn(context.Background(), "tarefa longa", nil); err == nil {
		t.Fatal("esperava erro de limite de passos")
	}
	if !a.HasCheckpoint() {
		t.Fatal("checkpoint não foi gravado")
	}

	f.Push(testutil.Text(`{"action":"final","answer":"concluído"}`))
	out, err := a.Continue(context.Background(), nil)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if out != "concluído" {
		t.Fatalf("resposta = %q", out)
	}
	h := a.History()
	if !strings.Contains(h, "tarefa longa") {
		t.Fatalf("histórico restaurado não contém a tarefa original: %q", h)
	}
	if !strings.Contains(h, "ERRO (read_file)") {
		t.Fatalf("histórico restaurado não contém as observações: %q", h)
	}
	if a.HasCheckpoint() {
		t.Fatal("checkpoint continuou pendente após turno bem-sucedido")
	}
}

// TestBehaviorContinueSemCheckpointFalha caracteriza o erro explícito quando
// não há nada para retomar (o /continue não inventa trabalho).
func TestBehaviorContinueSemCheckpointFalha(t *testing.T) {
	f := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"nunca usado"}`))
	a, dir := novaCenario(t, f, nil)
	comStore(t, a, dir)

	_, err := a.Continue(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "nenhum checkpoint pendente") {
		t.Fatalf("erro = %v, esperava 'nenhum checkpoint pendente'", err)
	}
}

// TestBehaviorResumeTaskReaproveitaHistoricoEmMemoria caracteriza o /continue
// sem checkpoint: segue com o histórico em memória em vez de falhar.
func TestBehaviorResumeTaskReaproveitaHistoricoEmMemoria(t *testing.T) {
	f := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"retomado"}`))
	a, dir := novaCenario(t, f, nil)
	comStore(t, a, dir)

	out, err := a.ResumeTask(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if out != "retomado" {
		t.Fatalf("resposta = %q", out)
	}
	if h := a.History(); !strings.Contains(h, "Continue a tarefa anterior") {
		t.Fatalf("prompt de continuação não foi enviado: %q", h)
	}
}

// TestBehaviorStreamingQueFalhaCaiNaRespostaDoJSON caracteriza o fallback do
// finishFinal: se o streaming falhar, vale a resposta já produzida na decisão.
func TestBehaviorStreamingQueFalhaCaiNaRespostaDoJSON(t *testing.T) {
	cfg := &config.Config{MaxSteps: 2, Stream: true}
	f := testutil.NewFakeLLM(
		testutil.Text(`{"action":"final","answer":"do json"}`),
		testutil.Fail(errors.New("stream caiu")),
	)
	a, _ := novaCenario(t, f, cfg)

	var deltas []string
	out, err := a.Turn(context.Background(), "oi", func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "do json" {
		t.Fatalf("resposta = %q", out)
	}
	if len(deltas) != 1 || deltas[0] != "do json" {
		t.Fatalf("deltas = %q", deltas)
	}
}

// TestBehaviorHistoryFormato congela o formato de History(): pares
// "[papel] conteúdo", sem o system prompt, com corte em 300 runas.
func TestBehaviorHistoryFormato(t *testing.T) {
	f := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"oi"}`))
	a, _ := novaCenario(t, f, nil)
	if got := a.History(); got != "(histórico vazio)" {
		t.Fatalf("histórico vazio = %q", got)
	}
	if _, err := a.Turn(context.Background(), "olá", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if got, want := a.History(), "[user] olá\n[assistant] oi\n"; got != want {
		t.Fatalf("histórico = %q, esperava %q", got, want)
	}

	longo := strings.Repeat("a", 350)
	f2 := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"ok"}`))
	a2, _ := novaCenario(t, f2, nil)
	if _, err := a2.Turn(context.Background(), longo, nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	h := a2.History()
	if !strings.Contains(h, "…") {
		t.Fatalf("histórico não marca o corte: %q", h)
	}
	if strings.Contains(h, longo) {
		t.Fatal("histórico não truncou a mensagem longa")
	}
}

// TestBehaviorSessoesEShowSession congela a apresentação das sessões: as
// mensagens com persistência desativada, a listagem e os turnos de uma sessão.
func TestBehaviorSessoesEShowSession(t *testing.T) {
	const desativada = "(persistência desativada; rode com -store <arquivo>)"

	f := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"oi"}`))
	semStore, _ := novaCenario(t, f, nil)
	if got := semStore.Sessions(); got != desativada {
		t.Fatalf("Sessions sem store = %q", got)
	}
	if got := semStore.ShowSession(""); got != desativada {
		t.Fatalf("ShowSession sem store = %q", got)
	}

	f2 := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"oi"}`))
	a, dir := novaCenario(t, f2, nil)
	comStore(t, a, dir)
	if _, err := a.Turn(context.Background(), "olá", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	lista := a.Sessions()
	if !strings.Contains(lista, a.SessionID()) || !strings.Contains(lista, "1 turno(s)") {
		t.Fatalf("Sessions = %q", lista)
	}
	turnos := a.ShowSession("")
	if !strings.Contains(turnos, "você> olá") || !strings.Contains(turnos, "assistente> oi") {
		t.Fatalf("ShowSession = %q", turnos)
	}
	if got, want := a.ShowSession("nao-existe"), "(sessão \"nao-existe\" não encontrada)"; got != want {
		t.Fatalf("ShowSession(inexistente) = %q, esperava %q", got, want)
	}
}

// TestBehaviorTrimHistoryViraResumoDeMemoria caracteriza o B-12/A-01: o que é
// descartado pelo orçamento de contexto não se perde — vira o resumo injetado
// logo após o system prompt. O teste olha o que foi enviado ao modelo (wire),
// não o campo privado.
func TestBehaviorTrimHistoryViraResumoDeMemoria(t *testing.T) {
	cfg := &config.Config{MaxSteps: 2, MaxContextTokens: 20}
	f := testutil.NewFakeLLM(
		testutil.Text(`{"action":"final","answer":"r1"}`),
		testutil.Text(`{"action":"final","answer":"r2"}`),
	)
	a, _ := novaCenario(t, f, cfg)

	primeira := "primeira mensagem " + strings.Repeat("longa ", 40)
	segunda := "segunda mensagem " + strings.Repeat("longa ", 40)
	if _, err := a.Turn(context.Background(), primeira, nil); err != nil {
		t.Fatalf("Turn 1: %v", err)
	}
	if _, err := a.Turn(context.Background(), segunda, nil); err != nil {
		t.Fatalf("Turn 2: %v", err)
	}

	reqs := f.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requisições = %d, esperava 2", len(reqs))
	}
	msgs := reqs[1].Messages
	if len(msgs) < 3 {
		t.Fatalf("mensagens do turno 2 = %d, esperava system + resumo + entrada", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("primeira mensagem = %q, esperava system", msgs[0].Role)
	}
	if !strings.HasPrefix(msgs[1].Content, "MEMÓRIA DA CONVERSA") {
		t.Fatalf("segunda mensagem não é o resumo: %q", msgs[1].Content)
	}
	for _, m := range msgs {
		if m.Content == primeira {
			t.Fatal("a mensagem descartada continuou no contexto (não virou resumo)")
		}
	}
}

// TestBehaviorNotasDeMemoriaEntramNoContexto caracteriza a memória de longo
// prazo: Remember injeta as notas logo após o system prompt e Memory() as
// mostra; Forget limpa as notas.
func TestBehaviorNotasDeMemoriaEntramNoContexto(t *testing.T) {
	f := testutil.NewFakeLLM(testutil.Text(`{"action":"final","answer":"ok"}`))
	a, _ := novaCenario(t, f, nil)

	if err := a.Remember("gosto de café"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if _, err := a.Turn(context.Background(), "oi", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	req, ok := f.LastRequest()
	if !ok {
		t.Fatal("nenhuma requisição registrada")
	}
	if len(req.Messages) < 2 || !strings.HasPrefix(req.Messages[1].Content, "FATOS LEMBRADOS") {
		t.Fatalf("notas não foram injetadas após o system prompt: %+v", req.Messages)
	}
	if !strings.Contains(req.Messages[1].Content, "gosto de café") {
		t.Fatalf("nota ausente do contexto: %q", req.Messages[1].Content)
	}
	want := "notas persistentes:\n  - gosto de café\nresumo da conversa:\n  (vazio)\n"
	if got := a.Memory(); got != want {
		t.Fatalf("Memory = %q, esperava %q", got, want)
	}
	a.Forget()
	if got := a.Memory(); !strings.Contains(got, "(nenhuma)") {
		t.Fatalf("Memory após Forget = %q", got)
	}
}
