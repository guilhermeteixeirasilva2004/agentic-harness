package cognition

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppendPreencheMetadados(t *testing.T) {
	tr := New("t1")
	s := tr.Append(Step{Kind: KindThought, Reasoning: "penso, logo existo"})
	if s.N != 1 {
		t.Fatalf("N = %d, esperava 1", s.N)
	}
	if s.At.IsZero() {
		t.Fatal("At não foi preenchido")
	}
	if s.Tokens == 0 {
		t.Fatal("Tokens não foi estimado")
	}
	tr.Append(Step{Kind: KindFinal, Answer: "ok"})
	if tr.Len() != 2 {
		t.Fatalf("Len = %d, esperava 2", tr.Len())
	}
	if tr.Steps[1].N != 2 {
		t.Fatalf("N da segunda etapa = %d, esperava 2", tr.Steps[1].N)
	}
}

func TestLastEPlan(t *testing.T) {
	tr := New("t")
	tr.Append(Step{Kind: KindPlan, Plan: []string{"a", "b"}})
	tr.Append(Step{Kind: KindThought, Reasoning: "x"})
	tr.Append(Step{Kind: KindPlan, Plan: []string{"c"}})

	if got := tr.Plan(); len(got) != 1 || got[0] != "c" {
		t.Fatalf("Plan = %v, esperava [c]", got)
	}
	if got := len(tr.Last(2)); got != 2 {
		t.Fatalf("Last(2) = %d, esperava 2", got)
	}
	if got := len(tr.Last(0)); got != 3 {
		t.Fatalf("Last(0) = %d, esperava 3", got)
	}
}

func TestEstimateTokens(t *testing.T) {
	cases := map[string]int{
		"":         0,
		"   ":      0,
		"abcd":     1,
		"abcde":    2,
		"abcdefgh": 2,
	}
	for in, want := range cases {
		if got := EstimateTokens(in); got != want {
			t.Fatalf("EstimateTokens(%q) = %d, esperava %d", in, got, want)
		}
	}
}

func TestBudget(t *testing.T) {
	weights := []int{10, 10, 10, 10}
	if got := Budget(weights, 0); got != 0 {
		t.Fatalf("Budget(0) = %d, esperava 0 (desativado)", got)
	}
	if got := Budget(weights, 40); got != 0 {
		t.Fatalf("Budget(40) = %d, esperava 0", got)
	}
	if got := Budget(weights, 25); got != 2 {
		t.Fatalf("Budget(25) = %d, esperava 2", got)
	}
	if got := Budget(weights, 5); got != 4 {
		t.Fatalf("Budget(5) = %d, esperava 4", got)
	}
}

func TestCompactLimpaEtapasAntigasEPreservaFinal(t *testing.T) {
	tr := New("t")
	big := strings.Repeat("x", 400) // ~100 tokens
	tr.Append(Step{Kind: KindFinal, Answer: big})
	for i := 0; i < 8; i++ {
		tr.Append(Step{Kind: KindThought, Reasoning: big})
	}
	before := tr.Tokens()
	tr.Compact(300, 3)

	if tr.Tokens() >= before {
		t.Fatalf("tokens não diminuíram: antes=%d depois=%d", before, tr.Tokens())
	}
	if tr.Steps[0].Answer == "" {
		t.Fatal("resposta final antiga deveria ser preservada")
	}
	if tr.Steps[1].Reasoning != "" {
		t.Fatal("raciocínio antigo deveria ser limpo")
	}
	if tr.Steps[len(tr.Steps)-1].Reasoning == "" {
		t.Fatal("as keepLast etapas mais recentes deveriam ser preservadas")
	}
}

func TestCompactDesativado(t *testing.T) {
	tr := New("t")
	tr.Append(Step{Kind: KindThought, Reasoning: strings.Repeat("x", 400)})
	before := tr.Tokens()
	tr.Compact(0, 1)
	if tr.Tokens() != before {
		t.Fatal("Compact(0) não deveria alterar o trace")
	}
}

func TestRenderMarkdown(t *testing.T) {
	tr := New("t")
	tr.Append(Step{Kind: KindPlan, Plan: []string{"ver hora", "responder"}})
	tr.Append(Step{Kind: KindToolCall, Tool: "get_time", Arguments: json.RawMessage(`{}`)})
	tr.Append(Step{Kind: KindObservation, Tool: "get_time", Observation: "12:00"})
	tr.Append(Step{Kind: KindFinal, Answer: "12 horas"})

	out := tr.RenderMarkdown()
	for _, want := range []string{"trace t", "plan", "ver hora", "get_time {}", "observation (get_time)", "12:00", "12 horas"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render não contém %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{\n") {
		t.Fatalf("argumentos deveriam ser renderizados em uma linha:\n%s", out)
	}
}
