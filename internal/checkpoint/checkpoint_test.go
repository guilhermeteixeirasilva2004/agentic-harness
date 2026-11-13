package checkpoint

import (
	"encoding/json"
	"testing"

	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
)

func payload(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func turn(erro string) map[string]string {
	return map[string]string{"input": "olá", "error": erro}
}

func TestPendingSemRegistros(t *testing.T) {
	if _, _, ok := Pending(nil, "s1", false); ok {
		t.Fatal("sem registros não há checkpoint")
	}
}

func TestPendingAchaCheckpoint(t *testing.T) {
	recs := []store.Record{
		{Type: "turn", Session: "s1", Payload: payload(t, turn(""))},
		{Type: "checkpoint", Session: "s1", Payload: payload(t, Record{Input: "tarefa", Strategy: "plan", Plan: []string{"a"}})},
	}
	cp, session, ok := Pending(recs, "s1", true)
	if !ok {
		t.Fatal("checkpoint deveria estar pendente")
	}
	if cp.Input != "tarefa" || len(cp.Plan) != 1 || session != "s1" {
		t.Fatalf("checkpoint = %+v (sessão %q)", cp, session)
	}
}

func TestPendingConsomeCheckpointComTurnoBemSucedido(t *testing.T) {
	recs := []store.Record{
		{Type: "checkpoint", Session: "s1", Payload: payload(t, Record{Input: "tarefa"})},
		{Type: "turn", Session: "s1", Payload: payload(t, turn(""))},
	}
	if _, _, ok := Pending(recs, "s1", true); ok {
		t.Fatal("turno bem-sucedido deveria consumir o checkpoint")
	}
}

func TestPendingMantemCheckpointAposTurnoComErro(t *testing.T) {
	recs := []store.Record{
		{Type: "checkpoint", Session: "s1", Payload: payload(t, Record{Input: "tarefa"})},
		{Type: "turn", Session: "s1", Payload: payload(t, turn("limite de passos"))},
	}
	if _, _, ok := Pending(recs, "s1", true); !ok {
		t.Fatal("turno com erro não deveria consumir o checkpoint")
	}
}

func TestPendingIgnoraTurnoDeOutraSessao(t *testing.T) {
	recs := []store.Record{
		{Type: "checkpoint", Session: "s1", Payload: payload(t, Record{Input: "tarefa"})},
		{Type: "turn", Session: "s2", Payload: payload(t, turn(""))},
	}
	if _, _, ok := Pending(recs, "s1", false); !ok {
		t.Fatal("turno de outra sessão não consome o checkpoint")
	}
}

func TestPendingSessaoExplicita(t *testing.T) {
	recs := []store.Record{
		{Type: "checkpoint", Session: "s1", Payload: payload(t, Record{Input: "um"})},
		{Type: "checkpoint", Session: "s2", Payload: payload(t, Record{Input: "dois"})},
	}
	cp, session, ok := Pending(recs, "s1", true)
	if !ok || cp.Input != "um" || session != "s1" {
		t.Fatalf("sessão explícita = %+v/%q/%v", cp, session, ok)
	}
	// Sem sessão fixada, vale o checkpoint mais recente.
	cp, session, ok = Pending(recs, "s1", false)
	if !ok || cp.Input != "dois" || session != "s2" {
		t.Fatalf("último checkpoint = %+v/%q/%v", cp, session, ok)
	}
}

func TestPendingIgnoraPayloadInvalido(t *testing.T) {
	recs := []store.Record{
		{Type: "checkpoint", Session: "s1", Payload: json.RawMessage("{inválido")},
	}
	if _, _, ok := Pending(recs, "s1", true); ok {
		t.Fatal("payload inválido deveria ser ignorado")
	}
}

func TestPendingRestauraHistorico(t *testing.T) {
	rec := Record{
		Input:   "tarefa",
		History: []llm.Message{{Role: llm.RoleSystem, Content: "system"}, {Role: llm.RoleUser, Content: "OBSERVAÇÃO (get_time): 12:00"}},
		Memory:  []string{"- você: algo"},
		Notes:   []string{"usa Go"},
	}
	cp, _, ok := Pending([]store.Record{{Type: "checkpoint", Session: "s1", Payload: payload(t, rec)}}, "s1", true)
	if !ok {
		t.Fatal("checkpoint deveria estar pendente")
	}
	if len(cp.History) != 2 || cp.History[0].Role != llm.RoleSystem || len(cp.Memory) != 1 || len(cp.Notes) != 1 {
		t.Fatalf("estado restaurado = %+v", cp)
	}
}
