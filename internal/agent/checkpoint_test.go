package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
)

// openTestStore abre um store temporário e o fecha ao fim do teste.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCheckpointGravadoAoEsgotarPassos(t *testing.T) {
	st := openTestStore(t)
	f := &fakeLLM{responses: []string{`{"action":"tool","tool":"get_time","arguments":{}}`}}
	a := newTestAgent(f, 1)
	a.SetStore(st)
	a.SetSession("cp-grava")

	if _, err := a.Turn(context.Background(), "que horas são?", nil); err == nil {
		t.Fatal("esperava erro de limite de passos")
	}

	recs, err := st.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var cp checkpointRecord
	found := false
	for _, r := range recs {
		if r.Type != "checkpoint" || r.Session != "cp-grava" {
			continue
		}
		if err := json.Unmarshal(r.Payload, &cp); err != nil {
			t.Fatalf("checkpoint inválido: %v", err)
		}
		found = true
	}
	if !found {
		t.Fatalf("checkpoint não gravado: %+v", recs)
	}
	if cp.Input != "que horas são?" {
		t.Fatalf("input do checkpoint = %q", cp.Input)
	}
	var observed bool
	for _, m := range cp.History {
		if strings.Contains(m.Content, "OBSERVAÇÃO") {
			observed = true
		}
	}
	if !observed {
		t.Fatalf("checkpoint sem as observações coletadas: %+v", cp.History)
	}
	if !a.HasCheckpoint() {
		t.Fatal("HasCheckpoint deveria ser verdadeiro após esgotar os passos")
	}
}

func TestContinueRestauraEConclui(t *testing.T) {
	st := openTestStore(t)

	// Primeiro "processo": interrompe por limite de passos e grava o checkpoint.
	f1 := &fakeLLM{responses: []string{`{"action":"tool","tool":"get_time","arguments":{}}`}}
	a1 := newTestAgent(f1, 1)
	a1.SetStore(st)
	a1.SetSession("cp-continua")
	if _, err := a1.Turn(context.Background(), "prepare o relatório", nil); err == nil {
		t.Fatal("esperava erro de limite de passos")
	}

	// Segundo "processo": agente novo, mesmo store, retoma do checkpoint.
	f2 := &fakeLLM{responses: []string{`{"action":"final","answer":"relatório pronto"}`}}
	a2 := newTestAgent(f2, 4)
	a2.SetStore(st)
	a2.SetSession("cp-continua")
	if !a2.HasCheckpoint() {
		t.Fatal("o novo agente deveria ver o checkpoint pendente")
	}

	out, err := a2.Continue(context.Background(), nil)
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if out != "relatório pronto" {
		t.Fatalf("out = %q", out)
	}
	if len(f2.requests) != 1 {
		t.Fatalf("requisições = %d, esperava 1", len(f2.requests))
	}
	var sawObservation, sawContinue bool
	for _, m := range f2.requests[0].Messages {
		if strings.Contains(m.Content, "OBSERVAÇÃO") {
			sawObservation = true
		}
		if m.Content == continuePrompt {
			sawContinue = true
		}
	}
	if !sawObservation {
		t.Fatalf("a continuação não recebeu as observações do checkpoint: %+v", f2.requests[0].Messages)
	}
	if !sawContinue {
		t.Fatal("a instrução de continuar não foi enviada ao modelo")
	}
	if a2.HasCheckpoint() {
		t.Fatal("o checkpoint deveria ter sido consumido por um turno bem-sucedido")
	}
}

// ResumeTask sem checkpoint pendente reaproveita o histórico em memória, sem
// exigir que a UI conheça o prompt de retomada (B-10).
func TestResumeTaskSemCheckpointUsaHistorico(t *testing.T) {
	st := openTestStore(t)
	f := &fakeLLM{responses: []string{`{"action":"final","answer":"continuei"}`}}
	a := newTestAgent(f, 4)
	a.SetStore(st)

	out, err := a.ResumeTask(context.Background(), nil)
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if out != "continuei" {
		t.Fatalf("out = %q", out)
	}
	if len(f.requests) != 1 {
		t.Fatalf("requisições = %d, esperava 1", len(f.requests))
	}
	found := false
	for _, m := range f.requests[0].Messages {
		if m.Content == continuePrompt {
			found = true
		}
	}
	if !found {
		t.Fatalf("a instrução de retomada não foi enviada: %+v", f.requests[0].Messages)
	}
}

func TestContinueSemStore(t *testing.T) {
	a := newTestAgent(&fakeLLM{}, 4)
	if _, err := a.Continue(context.Background(), nil); err == nil {
		t.Fatal("Continue sem store deveria falhar")
	}
	if a.HasCheckpoint() {
		t.Fatal("sem store não há checkpoint")
	}
}

func TestContinueSemCheckpoint(t *testing.T) {
	st := openTestStore(t)
	a := newTestAgent(&fakeLLM{}, 4)
	a.SetStore(st)
	if _, err := a.Continue(context.Background(), nil); err == nil {
		t.Fatal("Continue sem checkpoint deveria falhar")
	}
}

func TestCheckpointRestauraPlanoEEstrategia(t *testing.T) {
	cp := checkpointRecord{
		Input:    "tarefa",
		History:  []llm.Message{{Role: llm.RoleSystem, Content: "system"}},
		Memory:   []string{"- você: contexto anterior"},
		Notes:    []string{"o time usa Go"},
		Plan:     []string{"etapa 1", "etapa 2"},
		Strategy: "plan",
	}

	a := newTestAgent(&fakeLLM{}, 4)
	a.restoreCheckpoint(cp)

	if a.strategy.Name() != "plan" {
		t.Fatalf("estratégia = %q, esperava plan", a.strategy.Name())
	}
	if got := a.Trace(); !strings.Contains(got, "etapa 1") || !strings.Contains(got, "etapa 2") {
		t.Fatalf("plano restaurado não aparece no trace:\n%s", got)
	}
	if !strings.Contains(a.Memory(), "o time usa Go") {
		t.Fatalf("notas não restauradas:\n%s", a.Memory())
	}
}
