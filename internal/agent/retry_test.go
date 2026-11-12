package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/tools"
)

// flakyLLM falha nas primeiras N chamadas e depois responde normalmente.
type flakyLLM struct {
	fails int
	calls int
}

func (f *flakyLLM) Complete(_ context.Context, _ llm.ChatRequest) (string, error) {
	f.calls++
	if f.calls <= f.fails {
		return "", errors.New("servidor respondeu 503 Service Unavailable")
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
