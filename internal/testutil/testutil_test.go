package testutil

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentic-harness/internal/llm"
)

func TestFakeLLMComplete(t *testing.T) {
	f := NewFakeLLM(Text("primeira"), Text("segunda"))
	ctx := context.Background()

	got, err := f.Complete(ctx, llm.ChatRequest{Model: "m"})
	if err != nil || got != "primeira" {
		t.Fatalf("Complete = %q, %v; quero \"primeira\"", got, err)
	}
	got, err = f.Complete(ctx, llm.ChatRequest{Model: "m"})
	if err != nil || got != "segunda" {
		t.Fatalf("Complete = %q, %v; quero \"segunda\"", got, err)
	}
	if f.Pending() != 0 {
		t.Fatalf("Pending = %d; quero 0", f.Pending())
	}
	if _, err := f.Complete(ctx, llm.ChatRequest{}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("Complete sem fila: err = %v; quero ErrNoReply", err)
	}
}

func TestFakeLLMRecordsRequestsAndClonesMessages(t *testing.T) {
	f := NewFakeLLM(Text("ok"))
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "oi"}}
	req := llm.ChatRequest{Model: "m", Messages: msgs}

	if _, err := f.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// O agente reutiliza o backing array do histórico (B-11); o fake deve guardar
	// uma cópia, não a fatia original.
	msgs[0].Content = "alterado"

	got, ok := f.LastRequest()
	if !ok {
		t.Fatal("LastRequest não devolveu nada")
	}
	if got.Messages[0].Content != "oi" {
		t.Fatalf("requisição registrada mudou junto com o slice do chamador: %q", got.Messages[0].Content)
	}
	if n := len(f.Requests()); n != 1 {
		t.Fatalf("Requests = %d; quero 1", n)
	}
}

func TestFakeLLMChatStreamsChunks(t *testing.T) {
	f := NewFakeLLM(Reply{Content: "olá mundo", Chunks: []string{"olá", " ", "mundo"}})
	var got strings.Builder
	out, err := f.Chat(context.Background(), llm.ChatRequest{}, func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if out != "olá mundo" {
		t.Fatalf("saída = %q; quero \"olá mundo\"", out)
	}
	if got.String() != "olá mundo" {
		t.Fatalf("deltas = %q; quero \"olá mundo\"", got.String())
	}
}

func TestFakeLLMRespectsContextDuringDelay(t *testing.T) {
	f := NewFakeLLM(Reply{Content: "tarde demais", Delay: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Complete(ctx, llm.ChatRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; quero context.Canceled", err)
	}
}

func TestFakeLLMError(t *testing.T) {
	boom := errors.New("boom")
	f := NewFakeLLM(Fail(boom))
	if _, err := f.Complete(context.Background(), llm.ChatRequest{}); !errors.Is(err, boom) {
		t.Fatalf("err = %v; quero boom", err)
	}
}

func TestFakeLLMEmbed(t *testing.T) {
	f := NewFakeLLM()
	if _, err := f.Embed(context.Background(), llm.EmbeddingRequest{Input: "x"}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("Embed sem função: err = %v; quero ErrNoReply", err)
	}
	f.SetEmbedFunc(func(req llm.EmbeddingRequest) ([][]float32, error) {
		return [][]float32{{1, 0, 0}}, nil
	})
	vecs, err := f.Embed(context.Background(), llm.EmbeddingRequest{Input: "x"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 || vecs[0][0] != 1 {
		t.Fatalf("vetor inesperado: %v", vecs)
	}
	if n := len(f.EmbedRequests()); n != 2 {
		t.Fatalf("EmbedRequests = %d; quero 2", n)
	}
}

func TestFakeRepository(t *testing.T) {
	r := NewFakeRepository()
	if err := r.Append("turn", "s1", map[string]string{"input": "oi"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	recs := r.Records()
	if len(recs) != 1 || recs[0].Type != "turn" || recs[0].Session != "s1" {
		t.Fatalf("registros inesperados: %+v", recs)
	}
	if got := r.Payloads("turn"); len(got) != 1 || !strings.Contains(string(got[0]), "oi") {
		t.Fatalf("payloads: %v", got)
	}
	if r.Path() == "" {
		t.Fatal("Path vazio")
	}

	boom := errors.New("disco cheio")
	r.FailAppend(boom)
	if err := r.Append("turn", "s1", nil); !errors.Is(err, boom) {
		t.Fatalf("Append com falha = %v; quero boom", err)
	}
	r.FailAppend(nil)
	r.Reset()
	if len(r.Records()) != 0 {
		t.Fatal("Reset não limpou os registros")
	}
}

func TestClock(t *testing.T) {
	c := NewClock(time.Time{})
	if !c.Now().Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("agora = %v; quero 2024-01-01T00:00:00Z", c.Now())
	}
	c.Advance(90 * time.Minute)
	if got := c.Now(); !got.Equal(time.Date(2024, 1, 1, 1, 30, 0, 0, time.UTC)) {
		t.Fatalf("após Advance = %v", got)
	}
	c.Set(time.Unix(0, 0))
	if !c.Now().Equal(time.Unix(0, 0)) {
		t.Fatalf("após Set = %v", c.Now())
	}
}

func TestApproverRecorder(t *testing.T) {
	ap := NewApprover[string](true)
	ctx := context.Background()
	ok, err := ap.Approve(ctx, "write_file")
	if err != nil || !ok {
		t.Fatalf("Approve = %v, %v; quero true, nil", ok, err)
	}
	if ap.Count() != 1 || ap.Requests()[0] != "write_file" {
		t.Fatalf("solicitações: %v", ap.Requests())
	}
	boom := errors.New("sem terminal")
	ap.SetAnswer(false, boom)
	if _, err := ap.Approve(ctx, "run_shell"); !errors.Is(err, boom) {
		t.Fatalf("err = %v; quero boom", err)
	}

	fn := ApproverFunc[string](func(context.Context, string) (bool, error) { return false, nil })
	if ok, err := fn.Approve(ctx, "x"); err != nil || ok {
		t.Fatalf("ApproverFunc = %v, %v; quero false, nil", ok, err)
	}
}

func TestAsker(t *testing.T) {
	as := NewAsker("42")
	ctx := context.Background()
	got, err := as.Ask(ctx, "a resposta?")
	if err != nil || got != "42" {
		t.Fatalf("Ask = %q, %v", got, err)
	}
	if qs := as.Questions(); len(qs) != 1 || qs[0] != "a resposta?" {
		t.Fatalf("perguntas: %v", qs)
	}
	if _, err := as.Ask(ctx, "outra?"); !errors.Is(err, ErrNoReply) {
		t.Fatalf("err = %v; quero ErrNoReply", err)
	}
	as.SetErr(errors.New("eof"))
	if _, err := as.Ask(ctx, "mais?"); err == nil {
		t.Fatal("SetErr não teve efeito")
	}

	fn := AskerFunc(func(context.Context, string) (string, error) { return "sim", nil })
	if got, err := fn.Ask(ctx, "ok?"); err != nil || got != "sim" {
		t.Fatalf("AskerFunc = %q, %v", got, err)
	}
}
