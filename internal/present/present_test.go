package present

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
)

func TestHistory(t *testing.T) {
	if got := History(nil); got != "(histórico vazio)" {
		t.Fatalf("History(vazio) = %q", got)
	}
	if got := History([]llm.Message{{Role: llm.RoleSystem, Content: "system"}}); got != "(histórico vazio)" {
		t.Fatalf("History(só system) = %q", got)
	}
	got := History([]llm.Message{
		{Role: llm.RoleSystem, Content: "system"},
		{Role: llm.RoleUser, Content: "olá\nmundo"},
		{Role: llm.RoleAssistant, Content: "oi"},
	})
	want := "[user] olá mundo\n[assistant] oi\n"
	if got != want {
		t.Fatalf("History = %q, esperava %q", got, want)
	}
}

func TestHistoryTruncaPorRunas(t *testing.T) {
	got := History([]llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("á", 400)}})
	if !strings.HasSuffix(got, "…\n") {
		t.Fatalf("texto longo deveria ser cortado com …: %q", got)
	}
	if n := len([]rune(strings.TrimSuffix(strings.TrimPrefix(got, "[user] "), "\n"))); n != 301 {
		t.Fatalf("corte = %d runas, esperava 301 (300 + …)", n)
	}
}

func TestSessions(t *testing.T) {
	if got := Sessions("s.jsonl", nil, ""); got != "(nenhuma sessão persistida)" {
		t.Fatalf("Sessions(vazio) = %q", got)
	}
	base := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	recs := []store.Record{
		{Type: "turn", Session: "a", At: base},
		{Type: "turn", Session: "b", At: base.Add(2 * time.Hour)},
		{Type: "turn", Session: "b", At: base.Add(3 * time.Hour)},
		{Type: "checkpoint", Session: "c", At: base.Add(4 * time.Hour)},
	}
	got := Sessions("/tmp/s.jsonl", recs, "a")
	linhas := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(linhas) != 3 {
		t.Fatalf("Sessions = %q, esperava cabeçalho + 2 sessões", got)
	}
	if linhas[0] != "sessões persistidas em /tmp/s.jsonl:" {
		t.Fatalf("cabeçalho = %q", linhas[0])
	}
	if !strings.Contains(linhas[1], "b") || !strings.Contains(linhas[1], "2 turno(s)") {
		t.Fatalf("sessão mais recente deveria vir primeiro: %q", linhas[1])
	}
	if !strings.Contains(linhas[2], "a") || !strings.HasSuffix(linhas[2], " *") {
		t.Fatalf("sessão atual deveria ser marcada com *: %q", linhas[2])
	}
}

func TestShowSession(t *testing.T) {
	payload := func(v any) json.RawMessage {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return b
	}
	at := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	recs := []store.Record{
		{Type: "turn", Session: "s1", At: at, Payload: payload(map[string]string{"input": "olá"})},
		{Type: "turn", Session: "s1", At: at.Add(time.Minute), Payload: payload(map[string]string{"input": "de novo", "output": "oi", "error": "falhou"})},
		{Type: "turn", Session: "s2", At: at, Payload: payload(map[string]string{"input": "outra"})},
		{Type: "turn", Session: "s1", At: at, Payload: json.RawMessage("{inválido")},
	}

	if got, want := ShowSession(recs, "nao-existe"), "(sessão \"nao-existe\" não encontrada)"; got != want {
		t.Fatalf("ShowSession(inexistente) = %q, esperava %q", got, want)
	}
	got := ShowSession(recs, "s1")
	if strings.Contains(got, "outra") {
		t.Fatalf("turnos de outra sessão apareceram: %q", got)
	}
	for _, want := range []string{"#1 ", "#2 ", "você> olá", "você> de novo", "assistente> oi", "erro: falhou"} {
		if !strings.Contains(got, want) {
			t.Fatalf("ShowSession sem %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "#3 ") {
		t.Fatalf("payload inválido deveria ser ignorado:\n%s", got)
	}
	if strings.Contains(ShowSession(recs, "s1"), "assistente> \n") {
		t.Fatal("turno sem saída não deveria imprimir a linha do assistente")
	}
}

func TestShowSessionTruncaCampos(t *testing.T) {
	long := strings.Repeat("x", 600)
	b, err := json.Marshal(map[string]string{"input": long, "output": long})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	recs := []store.Record{{Type: "turn", Session: "s", At: time.Now(), Payload: b}}
	got := ShowSession(recs, "s")
	if !strings.Contains(got, "você> "+strings.Repeat("x", 300)+"…") {
		t.Fatalf("input deveria ser cortado em 300 runas:\n%s", got)
	}
	if !strings.Contains(got, "assistente> "+strings.Repeat("x", 500)+"…") {
		t.Fatalf("output deveria ser cortado em 500 runas:\n%s", got)
	}
}
