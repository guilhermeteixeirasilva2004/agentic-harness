package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/tools"
)

// flakyLLM falha nas primeiras N chamadas e depois responde normalmente. O erro
// das falhas é tipado (503 por padrão), como o cliente real devolve: a
// retentativa agora só repete erros transitórios (B-02).
type flakyLLM struct {
	fails int
	calls int
	err   error
}

func (f *flakyLLM) Complete(_ context.Context, _ llm.ChatRequest) (string, error) {
	f.calls++
	if f.calls <= f.fails {
		if f.err != nil {
			return "", f.err
		}
		return "", &llm.HTTPError{Status: 503, Body: "Service Unavailable"}
	}
	return `{"action":"final","answer":"ok"}`, nil
}

func newRetryAgent(f LLM, retries int) *Agent {
	cfg := &config.Config{System: "s", MaxSteps: 4, Retries: retries, Backoff: time.Millisecond}
	return New(cfg, f, tools.NewRegistry(), safety.New(4, nil))
}

func TestCompleteRetentaFalhasTransitorias(t *testing.T) {
	f := &flakyLLM{fails: 2}
	a := newRetryAgent(f, 2)

	out, err := a.Turn(context.Background(), "oi", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q", out)
	}
	if f.calls != 3 {
		t.Fatalf("chamadas = %d, esperava 3 (1 + 2 retentativas)", f.calls)
	}
	s := a.meter.Snapshot()
	if s.Retries != 2 || s.LLMCalls != 3 {
		t.Fatalf("Retries/LLMCalls = %d/%d, esperava 2/3", s.Retries, s.LLMCalls)
	}
	if !strings.Contains(a.Metrics(), "retentativas: 2") {
		t.Fatalf("resumo sem retentativas:\n%s", a.Metrics())
	}
}

func TestCompleteDesisteAposTentativas(t *testing.T) {
	f := &flakyLLM{fails: 99}
	a := newRetryAgent(f, 1)

	if _, err := a.Turn(context.Background(), "oi", nil); err == nil {
		t.Fatal("esperava erro após esgotar as tentativas")
	}
	if f.calls != 2 {
		t.Fatalf("chamadas = %d, esperava 2", f.calls)
	}
	if _, err := a.Turn(context.Background(), "oi", nil); err == nil {
		t.Fatal("esperava erro novamente")
	}
}

func TestCompleteNaoRetentaCancelamento(t *testing.T) {
	f := &flakyLLM{fails: 99}
	a := newRetryAgent(f, 3)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Turn(ctx, "oi", nil); err == nil {
		t.Fatal("esperava erro com contexto cancelado")
	}
	if f.calls != 1 {
		t.Fatalf("chamadas = %d, esperava 1 (sem retentativa)", f.calls)
	}
	if s := a.meter.Snapshot(); s.Retries != 0 {
		t.Fatalf("Retries = %d, esperava 0", s.Retries)
	}
}

func TestCompleteSemRetentativasQuandoDesligado(t *testing.T) {
	f := &flakyLLM{fails: 1}
	a := newRetryAgent(f, 0)

	if _, err := a.Turn(context.Background(), "oi", nil); err == nil {
		t.Fatal("esperava erro com -retries 0")
	}
	if f.calls != 1 {
		t.Fatalf("chamadas = %d, esperava 1", f.calls)
	}
}

// B-02: 4xx (pedido inválido) não é repetido; só o que é transitório é.
func TestCompleteNaoRetentaErroDePedido(t *testing.T) {
	f := &flakyLLM{fails: 1, err: &llm.HTTPError{Status: 400, Body: "modelo inexistente"}}
	a := newRetryAgent(f, 3)

	if _, err := a.Turn(context.Background(), "oi", nil); err == nil {
		t.Fatal("esperava erro")
	}
	if f.calls != 1 {
		t.Fatalf("chamadas = %d, esperava 1 (400 não deve repetir)", f.calls)
	}
	if s := a.meter.Snapshot(); s.Retries != 0 {
		t.Fatalf("Retries = %d, esperava 0", s.Retries)
	}
}

func TestCompleteRetentaTransporteE429(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("%w: connection reset", llm.ErrTransport),
		&llm.HTTPError{Status: 429, Body: "slow down"},
	} {
		f := &flakyLLM{fails: 1, err: err}
		a := newRetryAgent(f, 1)
		out, turnErr := a.Turn(context.Background(), "oi", nil)
		if turnErr != nil {
			t.Fatalf("%v: erro inesperado: %v", err, turnErr)
		}
		if out != "ok" || f.calls != 2 {
			t.Fatalf("%v: out = %q, chamadas = %d; esperava ok em 2 chamadas", err, out, f.calls)
		}
	}
}

func TestRetryable(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"transporte", fmt.Errorf("%w: dial tcp: refused", llm.ErrTransport), true},
		{"500", &llm.HTTPError{Status: 500}, true},
		{"503", &llm.HTTPError{Status: 503}, true},
		{"408", &llm.HTTPError{Status: 408}, true},
		{"429", &llm.HTTPError{Status: 429}, true},
		{"501", &llm.HTTPError{Status: 501}, false},
		{"400", &llm.HTTPError{Status: 400}, false},
		{"404", &llm.HTTPError{Status: 404}, false},
		{"protocolo", fmt.Errorf("%w: sem escolhas", llm.ErrProtocol), false},
		{"decode genérico", errors.New("json inválido"), false},
		{"cancelado", context.Canceled, false},
		{"prazo", context.DeadlineExceeded, false},
	}
	for _, tc := range cases {
		if got := retryable(ctx, tc.err); got != tc.want {
			t.Fatalf("%s: retryable = %v; quero %v", tc.name, got, tc.want)
		}
	}

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if retryable(canceled, fmt.Errorf("%w: x", llm.ErrTransport)) {
		t.Fatal("contexto cancelado não deveria permitir retentativa")
	}
}

func TestRetryAfterERespeitaTeto(t *testing.T) {
	if got := retryAfter(errors.New("nada")); got != 0 {
		t.Fatalf("sem HTTPError: %q", got)
	}
	if got := retryAfter(&llm.HTTPError{Status: 429, RetryAfter: 2 * time.Second}); got != 2*time.Second {
		t.Fatalf("RetryAfter = %s; quero 2s", got)
	}
	if got := retryAfter(&llm.HTTPError{Status: 429, RetryAfter: time.Hour}); got != maxRetryAfter {
		t.Fatalf("RetryAfter = %s; quero o teto %s", got, maxRetryAfter)
	}
}

func TestJittered(t *testing.T) {
	if got := jittered(0); got != 0 {
		t.Fatalf("jittered(0) = %s", got)
	}
	if got := jittered(time.Nanosecond); got != time.Nanosecond {
		t.Fatalf("jittered(ns) = %s; quero intacto", got)
	}
	base := time.Second
	for i := 0; i < 50; i++ {
		got := jittered(base)
		if got < base-base/5 || got > base+base/5 {
			t.Fatalf("jitter fora de ±20%%: %s", got)
		}
	}
}
