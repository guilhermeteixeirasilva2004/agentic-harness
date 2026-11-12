package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentic-harness/internal/cognition"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/tools"
)

// B-03: quando a montagem automática de argumentos falha, a tool não pode rodar
// "vazia": o erro real volta ao modelo e nada é executado.
func TestFillArgsFalhaNaoExecutaTool(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"get_time"}`,
		"isto não é JSON", // resposta da chamada de preenchimento
		`{"action":"final","answer":"fim"}`,
	}}
	a := newTestAgent(f, 5)

	out, err := a.Turn(context.Background(), "que horas são?", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "fim" {
		t.Fatalf("out = %q", out)
	}
	if len(f.requests) != 3 {
		t.Fatalf("requisições = %d, esperava 3 (decisão, args, decisão)", len(f.requests))
	}
	rf := f.requests[1].ResponseFormat
	if rf == nil || rf.JSONSchema == nil || rf.JSONSchema.Name != "arguments" {
		t.Fatalf("a segunda chamada deveria preencher os argumentos: %+v", rf)
	}

	msgs := f.requests[2].Messages
	if len(msgs) < 2 {
		t.Fatalf("histórico curto demais: %+v", msgs)
	}
	if msgs[len(msgs)-2].Role != llm.RoleAssistant || !strings.Contains(msgs[len(msgs)-2].Content, `"tool":"get_time"`) {
		t.Fatalf("a decisão original deveria continuar no histórico: %+v", msgs)
	}
	if last := msgs[len(msgs)-1].Content; !strings.Contains(last, "ERRO (args)") || !strings.Contains(last, "get_time") {
		t.Fatalf("erro de preenchimento não foi realimentado: %q", last)
	}
	for _, m := range msgs {
		if strings.Contains(m.Content, "OBSERVAÇÃO (get_time") {
			t.Fatalf("a tool não deveria ter sido executada: %+v", msgs)
		}
	}

	var sawArgsError bool
	for _, s := range a.steps.Steps {
		if s.Kind == cognition.KindError && strings.Contains(s.Observation, "ERRO (args)") {
			sawArgsError = true
		}
	}
	if !sawArgsError {
		t.Fatalf("trace sem etapa de erro de argumentos: %+v", a.steps.Steps)
	}
}

// S-05: o resumo mostrado ao humano (e auditado) traz os argumentos completos.
func TestApprovalSummaryNaoTrunca(t *testing.T) {
	long := strings.Repeat("a", 600)
	call := tools.Call{
		Tool:      "write_file",
		Arguments: json.RawMessage(`{"path":"a.txt","content":"` + long + `"}`),
	}
	summary := approvalSummary(call)
	if !strings.Contains(summary, long) {
		t.Fatalf("resumo truncou os argumentos (%d caracteres): %q", len(summary), summary)
	}
	if !strings.HasPrefix(summary, "write_file {") {
		t.Fatalf("resumo inesperado: %q", summary)
	}

	multi := tools.Call{Tool: "run_shell", Arguments: json.RawMessage("{\n\"cmd\":\"ls\"\n}")}
	if got := approvalSummary(multi); strings.Contains(got, "\n") {
		t.Fatalf("resumo deveria ser de uma linha: %q", got)
	}
	if got := approvalSummary(tools.Call{Tool: "get_time"}); got != "get_time" {
		t.Fatalf("resumo sem argumentos = %q", got)
	}
}
