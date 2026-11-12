package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/store"
	"agentic-harness/internal/tools"
)

// fakeLLM devolve respostas roteirizadas e registra as requisições recebidas.
type fakeLLM struct {
	responses []string
	requests  []llm.ChatRequest
}

func (f *fakeLLM) Complete(_ context.Context, req llm.ChatRequest) (string, error) {
	f.requests = append(f.requests, req)
	if len(f.responses) == 0 {
		return `{"action":"final","answer":"fim"}`, nil
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return r, nil
}

func newTestAgent(f *fakeLLM, maxSteps int) *Agent {
	cfg := &config.Config{System: "teste", MaxSteps: maxSteps}
	reg := tools.NewRegistry()
	reg.Add(tools.GetTime{})
	reg.Allow("get_time")
	return New(cfg, f, reg, safety.New(maxSteps, []string{"get_time"}))
}

func TestTurnRespostaFinalDireta(t *testing.T) {
	f := &fakeLLM{responses: []string{`{"action":"final","answer":"oi"}`}}
	a := newTestAgent(f, 4)

	var streamed strings.Builder
	out, err := a.Turn(context.Background(), "olá", func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "oi" || streamed.String() != "oi" {
		t.Fatalf("out=%q streamed=%q", out, streamed.String())
	}
	if len(f.requests) != 1 {
		t.Fatalf("requisições = %d, esperava 1", len(f.requests))
	}
	rf := f.requests[0].ResponseFormat
	if rf == nil || rf.Type != "json_schema" || rf.JSONSchema == nil {
		t.Fatalf("response_format inesperado: %+v", rf)
	}
	if _, ok := rf.JSONSchema.Schema["properties"]; !ok {
		t.Fatalf("schema sem properties: %+v", rf.JSONSchema.Schema)
	}
}

func TestTurnUsaToolEObserva(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"get_time","arguments":{}}`,
		`{"action":"final","answer":"pronto"}`,
	}}
	a := newTestAgent(f, 4)

	out, err := a.Turn(context.Background(), "que horas são?", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "pronto" {
		t.Fatalf("out = %q", out)
	}
	if len(f.requests) != 2 {
		t.Fatalf("requisições = %d, esperava 2", len(f.requests))
	}
	found := false
	for _, m := range f.requests[1].Messages {
		if strings.Contains(m.Content, "OBSERVAÇÃO") && strings.Contains(m.Content, "get_time") {
			found = true
		}
	}
	if !found {
		t.Fatalf("observação não foi injetada no histórico: %+v", f.requests[1].Messages)
	}
}

func TestTurnBloqueiaToolForaDaPolicy(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"read_file","arguments":{"path":"x"}}`,
		`{"action":"final","answer":"ok"}`,
	}}
	a := newTestAgent(f, 4)

	out, err := a.Turn(context.Background(), "leia x", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q", out)
	}
	found := false
	for _, m := range f.requests[1].Messages {
		if strings.Contains(m.Content, "ERRO") && strings.Contains(m.Content, "read_file") {
			found = true
		}
	}
	if !found {
		t.Fatalf("erro da política não foi realimentado: %+v", f.requests[1].Messages)
	}
}

func TestTurnLimitaPassos(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"get_time","arguments":{}}`,
		`{"action":"tool","tool":"get_time","arguments":{}}`,
		`{"action":"tool","tool":"get_time","arguments":{}}`,
	}}
	a := newTestAgent(f, 2)

	if _, err := a.Turn(context.Background(), "loop", nil); err == nil {
		t.Fatal("esperava erro de limite de passos")
	}
}

func TestTurnAceitaToolCallPuro(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"tool":"get_time","arguments":{}}`,
		`{"action":"final","answer":"feito"}`,
	}}
	a := newTestAgent(f, 4)

	out, err := a.Turn(context.Background(), "hora", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "feito" {
		t.Fatalf("out = %q", out)
	}
}

func TestTurnRealimentaErroDeArgumentos(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"read_file","arguments":{}}`,
		`{"action":"final","answer":"ok"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4}
	reg := tools.NewRegistry()
	reg.Add(tools.ReadFile{Root: t.TempDir()})
	reg.Allow("read_file")
	a := New(cfg, f, reg, safety.New(4, []string{"read_file"}))

	out, err := a.Turn(context.Background(), "leia", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q", out)
	}
	found := false
	for _, m := range f.requests[1].Messages {
		if strings.Contains(m.Content, "ERRO") && strings.Contains(m.Content, "path") {
			found = true
		}
	}
	if !found {
		t.Fatalf("erro detalhado não foi realimentado: %+v", f.requests[1].Messages)
	}
}

func TestTurnPreencheArgumentosComSchemaDaTool(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module exemplo"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"read_file"}`, // decisão sem argumentos
		`{"path":"go.mod"}`,                    // etapa 2: argumentos
		`{"action":"final","answer":"exemplo"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4}
	reg := tools.NewRegistry()
	reg.Add(tools.ReadFile{Root: dir})
	reg.Allow("read_file")
	a := New(cfg, f, reg, safety.New(4, []string{"read_file"}))

	out, err := a.Turn(context.Background(), "leia go.mod", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "exemplo" {
		t.Fatalf("out = %q", out)
	}
	if len(f.requests) != 3 {
		t.Fatalf("requisições = %d, esperava 3 (decisão + args + final)", len(f.requests))
	}
	rf := f.requests[1].ResponseFormat
	if rf == nil || rf.Type != "json_schema" || rf.JSONSchema == nil || rf.JSONSchema.Name != "arguments" {
		t.Fatalf("response_format da etapa de args inesperado: %+v", rf)
	}
	if _, ok := rf.JSONSchema.Schema["properties"]; !ok {
		t.Fatalf("schema de argumentos sem properties: %+v", rf.JSONSchema.Schema)
	}
	if len(f.requests[1].Messages) == 0 || !strings.Contains(f.requests[1].Messages[len(f.requests[1].Messages)-1].Content, "leia go.mod") {
		t.Fatalf("tarefa não foi enviada na etapa de argumentos: %+v", f.requests[1].Messages)
	}
	// a observação final deve conter o conteúdo lido
	found := false
	for _, m := range f.requests[2].Messages {
		if strings.Contains(m.Content, "module exemplo") {
			found = true
		}
	}
	if !found {
		t.Fatalf("conteúdo do arquivo não chegou como observação: %+v", f.requests[2].Messages)
	}
}

func historyContains(msgs []llm.Message, want string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, want) {
			return true
		}
	}
	return false
}

func TestTurnCoTRegistraRaciocinio(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"final","answer":"ok","reasoning":"pensei primeiro"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4, Strategy: "cot"}
	reg := tools.NewRegistry()
	a := New(cfg, f, reg, safety.New(4, nil))

	out, err := a.Turn(context.Background(), "oi", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q, esperava ok", out)
	}
	if strings.Contains(out, "pensei") {
		t.Fatal("raciocínio não deveria vazar para a resposta final")
	}
	if !strings.Contains(a.Trace(), "pensei primeiro") {
		t.Fatalf("raciocínio não foi registrado no trace:\n%s", a.Trace())
	}
	if a.Strategy() != "cot" {
		t.Fatalf("estratégia = %q, esperava cot", a.Strategy())
	}
	required := f.requests[0].ResponseFormat.JSONSchema.Schema["required"].([]string)
	found := false
	for _, r := range required {
		if r == "reasoning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("schema do cot deveria exigir reasoning: %v", required)
	}
}

func TestTurnPlanInjetaPlanoEExecuta(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"plan","plan":["ver a hora","responder"],"reasoning":"preciso da hora"}`,
		`{"action":"tool","tool":"get_time","arguments":{}}`,
		`{"action":"final","answer":"12h","reasoning":"pronto"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4, Strategy: "plan"}
	reg := tools.NewRegistry()
	reg.Add(tools.GetTime{})
	reg.Allow("get_time")
	a := New(cfg, f, reg, safety.New(4, []string{"get_time"}))

	out, err := a.Turn(context.Background(), "que horas são?", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "12h" {
		t.Fatalf("out = %q, esperava 12h", out)
	}
	if len(f.requests) != 3 {
		t.Fatalf("requisições = %d, esperava 3 (plan + tool + final)", len(f.requests))
	}
	if !historyContains(f.requests[1].Messages, "PLANO ATUAL") || !historyContains(f.requests[1].Messages, "ver a hora") {
		t.Fatalf("plano não foi injetado no histórico: %+v", f.requests[1].Messages)
	}
	// Primeiro passo só pode propor plano; depois, plan sai do enum.
	firstActions := f.requests[0].ResponseFormat.JSONSchema.Schema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	if len(firstActions) != 1 || firstActions[0] != "plan" {
		t.Fatalf("schema do primeiro passo = %v, esperava só plan", firstActions)
	}
	stepActions := f.requests[1].ResponseFormat.JSONSchema.Schema["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	for _, a := range stepActions {
		if a == "plan" {
			t.Fatalf("schema dos passos seguintes ainda permite plan: %v", stepActions)
		}
	}
	tr := a.Trace()
	if !strings.Contains(tr, "ver a hora") || !strings.Contains(tr, "get_time") {
		t.Fatalf("trace não registrou plano/tool:\n%s", tr)
	}
}

func TestTurnReplanSubstituiPlano(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"plan","plan":["passo A"],"reasoning":"a"}`,
		`{"action":"replan","plan":["passo B"],"reasoning":"b"}`,
		`{"action":"final","answer":"fim","reasoning":"c"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4, Strategy: "plan"}
	reg := tools.NewRegistry()
	a := New(cfg, f, reg, safety.New(4, nil))

	out, err := a.Turn(context.Background(), "tarefa", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "fim" {
		t.Fatalf("out = %q, esperava fim", out)
	}
	last := f.requests[len(f.requests)-1].Messages
	if historyContains(last, "passo A") {
		t.Fatalf("plano antigo deveria ter sido removido: %+v", last)
	}
	if !historyContains(last, "passo B") {
		t.Fatalf("plano revisado não está no histórico: %+v", last)
	}
}

func TestTurnPlanSemPlanoRealimentaErro(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"plan","reasoning":"esqueci o plano"}`,
		`{"action":"final","answer":"ok","reasoning":"fim"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4, Strategy: "plan"}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))

	out, err := a.Turn(context.Background(), "tarefa", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q, esperava ok", out)
	}
	if !historyContains(f.requests[1].Messages, "ERRO (plano)") {
		t.Fatalf("erro do plano não foi realimentado: %+v", f.requests[1].Messages)
	}
}

func TestTurnToolSemNomeRealimentaErro(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","reasoning":"esqueci o nome"}`,
		`{"action":"final","answer":"ok","reasoning":"fim"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4, Strategy: "cot"}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))

	out, err := a.Turn(context.Background(), "tarefa", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q, esperava ok", out)
	}
	if !historyContains(f.requests[1].Messages, "exige o campo \"tool\"") {
		t.Fatalf("erro de tool vazia não foi realimentado: %+v", f.requests[1].Messages)
	}
}

func TestTrimHistoryRespeitaOrcamento(t *testing.T) {
	f := &fakeLLM{}
	cfg := &config.Config{System: "s", MaxSteps: 4, MaxContextTokens: 10}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))
	a.history = append(a.history,
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("a", 400)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("b", 400)},
		llm.Message{Role: llm.RoleUser, Content: "recente"},
	)

	a.trimHistory()

	if len(a.history) != 3 {
		t.Fatalf("histórico = %d mensagens, esperava 3 (system + memória + recente): %+v", len(a.history), a.history)
	}
	if a.history[0].Role != llm.RoleSystem {
		t.Fatalf("system prompt foi descartado: %+v", a.history)
	}
	if !strings.HasPrefix(a.history[1].Content, memoryPrefix) {
		t.Fatalf("esperava o resumo na memória (posição 1): %+v", a.history)
	}
	if !strings.Contains(a.history[1].Content, "aaaa") {
		t.Fatalf("resumo não preservou o conteúdo descartado: %+v", a.history[1].Content)
	}
	if a.history[2].Content != "recente" {
		t.Fatalf("última mensagem não foi preservada: %+v", a.history)
	}
}

func TestClearResetaTrace(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"final","answer":"ok","reasoning":"pensei"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4, Strategy: "cot"}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))

	if _, err := a.Turn(context.Background(), "oi", nil); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(a.Trace(), "pensei") {
		t.Fatal("trace deveria conter o raciocínio antes do Clear")
	}
	a.Clear()
	if strings.Contains(a.Trace(), "pensei") {
		t.Fatal("Clear deveria resetar o trace")
	}
}

func TestTurnRegistraMetricas(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"get_time","arguments":{}}`,
		`{"action":"final","answer":"fim"}`,
	}}
	a := newTestAgent(f, 4)

	if _, err := a.Turn(context.Background(), "que horas?", nil); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	s := a.meter.Snapshot()
	if s.Turns != 1 {
		t.Fatalf("Turns = %d, esperava 1", s.Turns)
	}
	if s.Steps != 2 || s.LLMCalls != 2 {
		t.Fatalf("Steps/LLMCalls = %d/%d, esperava 2/2", s.Steps, s.LLMCalls)
	}
	if s.ToolCalls != 1 || s.ToolErrors != 0 {
		t.Fatalf("tools = %d chamadas, %d erros", s.ToolCalls, s.ToolErrors)
	}
	if s.TokensIn == 0 || s.TokensOut == 0 {
		t.Fatalf("tokens não registrados: %+v", s)
	}
	if !strings.Contains(a.Metrics(), "turnos:       1") {
		t.Fatalf("resumo inesperado:\n%s", a.Metrics())
	}
}

func TestTurnPersisteSessao(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	f := &fakeLLM{responses: []string{`{"action":"final","answer":"oi"}`}}
	a := newTestAgent(f, 4)
	a.SetStore(st)
	a.SetSession("sess-teste")

	if _, err := a.Turn(context.Background(), "olá", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	recs, err := st.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("registros = %d, esperava 1", len(recs))
	}
	if recs[0].Type != "turn" || recs[0].Session != "sess-teste" {
		t.Fatalf("registro = %+v", recs[0])
	}
	if !strings.Contains(string(recs[0].Payload), `"output":"oi"`) {
		t.Fatalf("payload sem saída: %s", recs[0].Payload)
	}
	if !strings.Contains(a.Sessions(), "sess-teste") {
		t.Fatalf("sessão não listada:\n%s", a.Sessions())
	}
	if !strings.Contains(a.ShowSession("sess-teste"), "olá") {
		t.Fatalf("ShowSession sem entrada:\n%s", a.ShowSession("sess-teste"))
	}
}

func TestResumeRestauraHistorico(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	f1 := &fakeLLM{responses: []string{`{"action":"final","answer":"resposta anterior"}`}}
	a1 := newTestAgent(f1, 4)
	a1.SetStore(st)
	a1.SetSession("sess-retomar")
	if _, err := a1.Turn(context.Background(), "pergunta anterior", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	a2 := newTestAgent(&fakeLLM{}, 4)
	a2.SetStore(st)
	if err := a2.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if a2.SessionID() != "sess-retomar" {
		t.Fatalf("SessionID = %q, esperava sess-retomar", a2.SessionID())
	}
	h := a2.History()
	if !strings.Contains(h, "pergunta anterior") || !strings.Contains(h, "resposta anterior") {
		t.Fatalf("histórico restaurado incompleto:\n%s", h)
	}
}

func TestResumeSemSessoes(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	a := newTestAgent(&fakeLLM{}, 4)
	a.SetStore(st)
	if err := a.Resume(); err == nil {
		t.Fatal("Resume sem sessões persistidas deveria falhar")
	}
}

func TestResumeSemStore(t *testing.T) {
	a := newTestAgent(&fakeLLM{}, 4)
	if err := a.Resume(); err == nil {
		t.Fatal("Resume sem store deveria falhar")
	}
}

func TestHistorySemSystem(t *testing.T) {
	f := &fakeLLM{responses: []string{`{"action":"final","answer":"oi"}`}}
	a := newTestAgent(f, 4)
	if got := a.History(); got != "(histórico vazio)" {
		t.Fatalf("History inicial = %q", got)
	}
	if _, err := a.Turn(context.Background(), "olá", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	h := a.History()
	if !strings.Contains(h, "[user] olá") || !strings.Contains(h, "[assistant] oi") {
		t.Fatalf("History = %q", h)
	}
}
