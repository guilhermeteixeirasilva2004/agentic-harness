package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentic-harness/internal/config"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/tools"
)

// fakeAsker registra as perguntas e devolve uma resposta roteirizada.
type fakeAsker struct {
	answer string
	err    error
	asked  []string
}

func (f *fakeAsker) Ask(_ context.Context, question string) (string, error) {
	f.asked = append(f.asked, question)
	if f.err != nil {
		return "", f.err
	}
	return f.answer, nil
}

func TestAskUserToolSemCanal(t *testing.T) {
	tool := askUserTool{a: newMemoryAgent()}

	if _, err := tool.Run(context.Background(), []byte(`{"question":"qual o nome?"}`)); err == nil {
		t.Fatal("esperava erro sem canal de interação")
	}
}

func TestAskUserToolDevolveResposta(t *testing.T) {
	a := newMemoryAgent()
	as := &fakeAsker{answer: "Maria"}
	a.SetAsker(as)
	tool := askUserTool{a: a}

	out, err := tool.Run(context.Background(), []byte(`{"question":"qual o nome?"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "Maria") {
		t.Fatalf("saída = %q", out)
	}
	if len(as.asked) != 1 || as.asked[0] != "qual o nome?" {
		t.Fatalf("perguntas = %#v", as.asked)
	}
}

func TestAskUserToolRespostaVazia(t *testing.T) {
	a := newMemoryAgent()
	a.SetAsker(&fakeAsker{answer: "  "})

	out, err := askUserTool{a: a}.Run(context.Background(), []byte(`{"question":"algo?"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "não respondeu") {
		t.Fatalf("saída = %q", out)
	}
}

func TestAskUserToolErros(t *testing.T) {
	a := newMemoryAgent()
	tool := askUserTool{a: a}

	if _, err := tool.Run(context.Background(), []byte(`{"question":""}`)); err == nil {
		t.Fatal("esperava erro para pergunta vazia")
	}
	if _, err := tool.Run(context.Background(), []byte(`{"question":`)); err == nil {
		t.Fatal("esperava erro para JSON inválido")
	}

	a.SetAsker(&fakeAsker{err: errors.New("entrada encerrada")})
	if _, err := tool.Run(context.Background(), []byte(`{"question":"e agora?"}`)); err == nil {
		t.Fatal("esperava erro repassado do asker")
	}
}

func TestAskUserNoLoopDoAgente(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"ask_user","arguments":{"question":"qual o projeto?"}}`,
		`{"action":"final","answer":"anotado"}`,
	}}
	cfg := &config.Config{System: "s", MaxSteps: 4}
	reg := tools.NewRegistry()
	reg.Allow("ask_user")
	a := New(cfg, f, reg, safety.New(4, []string{"ask_user"}))
	reg.Add(askUserTool{a: a})
	a.SetAsker(&fakeAsker{answer: "agentic-harness"})

	out, err := a.Turn(context.Background(), "preciso de ajuda", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "anotado" {
		t.Fatalf("out = %q", out)
	}
	found := false
	for _, m := range f.requests[1].Messages {
		if strings.Contains(m.Content, "agentic-harness") {
			found = true
		}
	}
	if !found {
		t.Fatalf("resposta do usuário não foi realimentada: %+v", f.requests[1].Messages)
	}
}
