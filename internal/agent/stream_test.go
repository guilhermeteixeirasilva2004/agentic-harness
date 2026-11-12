package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/tools"
)

// streamLLM implementa Complete (decisões, sem stream) e Chat (resposta final
// em streaming), permitindo exercitar o caminho de -stream sem HTTP.
type streamLLM struct {
	fakeLLM
	streamText string
	streamReqs int
	err        error
}

func (s *streamLLM) Chat(_ context.Context, _ llm.ChatRequest, onDelta func(string)) (string, error) {
	s.streamReqs++
	if s.err != nil {
		return "", s.err
	}
	for _, part := range strings.SplitAfter(s.streamText, " ") {
		if part != "" && onDelta != nil {
			onDelta(part)
		}
	}
	return s.streamText, nil
}

func newStreamAgent(ll LLM, stream bool) *Agent {
	cfg := &config.Config{System: "teste", MaxSteps: 4, Stream: stream}
	reg := tools.NewRegistry()
	reg.Add(tools.GetTime{})
	reg.Allow("get_time")
	return New(cfg, ll, reg, safety.New(4, []string{"get_time"}))
}

func TestFinalStreamingTokenAPartirDoModelo(t *testing.T) {
	f := &streamLLM{
		fakeLLM:    fakeLLM{responses: []string{`{"action":"final","answer":"curta"}`}},
		streamText: "resposta completa",
	}
	a := newStreamAgent(f, true)

	var streamed strings.Builder
	out, err := a.Turn(context.Background(), "oi", func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "resposta completa" || streamed.String() != "resposta completa" {
		t.Fatalf("out=%q streamed=%q", out, streamed.String())
	}
	if f.streamReqs != 1 {
		t.Fatalf("chamadas de streaming = %d, esperava 1", f.streamReqs)
	}
	if len(f.requests) != 1 || f.requests[0].Stream {
		t.Fatalf("a decisão deveria ser sem streaming: %+v", f.requests)
	}
	if !strings.Contains(a.History(), "resposta completa") {
		t.Fatalf("resposta final não foi para o histórico:\n%s", a.History())
	}
}

func TestFinalSemStreamUsaRespostaDoJSON(t *testing.T) {
	f := &streamLLM{
		fakeLLM:    fakeLLM{responses: []string{`{"action":"final","answer":"curta"}`}},
		streamText: "não deveria aparecer",
	}
	a := newStreamAgent(f, false)

	var streamed strings.Builder
	out, err := a.Turn(context.Background(), "oi", func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "curta" || streamed.String() != "curta" {
		t.Fatalf("out=%q streamed=%q", out, streamed.String())
	}
	if f.streamReqs != 0 {
		t.Fatalf("sem -stream não deveria chamar Chat: %d", f.streamReqs)
	}
}

func TestFinalStreamVazioCaiNaRespostaDoJSON(t *testing.T) {
	f := &streamLLM{fakeLLM: fakeLLM{responses: []string{`{"action":"final","answer":"curta"}`}}}
	a := newStreamAgent(f, true)

	var streamed strings.Builder
	out, err := a.Turn(context.Background(), "oi", func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "curta" || streamed.String() != "curta" {
		t.Fatalf("out=%q streamed=%q", out, streamed.String())
	}
	if f.streamReqs != 1 {
		t.Fatalf("chamadas de streaming = %d, esperava 1", f.streamReqs)
	}
}

func TestFinalStreamErroCaiNaRespostaDoJSON(t *testing.T) {
	f := &streamLLM{
		fakeLLM: fakeLLM{responses: []string{`{"action":"final","answer":"curta"}`}},
		err:     errors.New("stream quebrado"),
	}
	a := newStreamAgent(f, true)

	var streamed strings.Builder
	out, err := a.Turn(context.Background(), "oi", func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "curta" || streamed.String() != "curta" {
		t.Fatalf("out=%q streamed=%q", out, streamed.String())
	}
}

func TestFinalSemStreamerUsaRespostaDoJSON(t *testing.T) {
	// fakeLLM não implementa Chat: com -stream ligado, cai na resposta do JSON.
	f := &fakeLLM{responses: []string{`{"action":"final","answer":"direta"}`}}
	a := newStreamAgent(f, true)

	var streamed strings.Builder
	out, err := a.Turn(context.Background(), "oi", func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "direta" || streamed.String() != "direta" {
		t.Fatalf("out=%q streamed=%q", out, streamed.String())
	}
}
