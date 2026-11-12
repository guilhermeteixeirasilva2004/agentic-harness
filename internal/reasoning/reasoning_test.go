package reasoning

import (
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	cases := map[string]string{
		"":                 "react",
		"react":            "react",
		"cot":              "cot",
		"COT":              "cot",
		"chain-of-thought": "cot",
		"plan":             "plan",
		"plan-execute":     "plan",
		"desconhecida":     "react",
	}
	for in, want := range cases {
		if got := Get(in).Name(); got != want {
			t.Fatalf("Get(%q) = %q, esperava %q", in, got, want)
		}
	}
}

func requiredOf(s Strategy) []string {
	return requiredList(s.Schema())
}

func requiredList(schema map[string]any) []string {
	raw, ok := schema["required"]
	if !ok {
		return nil
	}
	return raw.([]string)
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

func TestSchemaPorEstrategia(t *testing.T) {
	if contains(requiredOf(Get("react")), "reasoning") {
		t.Fatal("react não deveria exigir reasoning")
	}
	if !contains(requiredOf(Get("cot")), "reasoning") {
		t.Fatal("cot deveria exigir reasoning")
	}
	if !contains(requiredOf(Get("plan")), "reasoning") {
		t.Fatal("plan deveria exigir reasoning")
	}
	// O campo tool é obrigatório para não perdermos a chamada (falha real com o 3B).
	for _, name := range Names() {
		if !contains(requiredOf(Get(name)), "tool") {
			t.Fatalf("%s deveria exigir o campo tool", name)
		}
	}

	props := Get("plan").Schema()["properties"].(map[string]any)
	if _, ok := props["plan"]; !ok {
		t.Fatal("plan deveria declarar o campo plan")
	}
	actions := props["action"].(map[string]any)["enum"].([]string)
	for _, want := range []string{"plan", "tool", "replan", "final"} {
		if !contains(actions, want) {
			t.Fatalf("enum de ações do plan não contém %q: %v", want, actions)
		}
	}

	reactActions := Get("react").Schema()["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	if contains(reactActions, "plan") || contains(reactActions, "replan") {
		t.Fatalf("react não deveria permitir plan/replan: %v", reactActions)
	}
}

func TestPlannerSchemas(t *testing.T) {
	p, ok := Get("plan").(Planner)
	if !ok {
		t.Fatal("a estratégia plan deveria implementar Planner")
	}
	planActions := p.PlanSchema()["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	if len(planActions) != 1 || planActions[0] != "plan" {
		t.Fatalf("PlanSchema deveria permitir só plan, veio %v", planActions)
	}
	stepActions := p.StepSchema()["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string)
	if contains(stepActions, "plan") {
		t.Fatalf("StepSchema não deveria permitir plan: %v", stepActions)
	}
	for _, want := range []string{"tool", "replan", "final"} {
		if !contains(stepActions, want) {
			t.Fatalf("StepSchema não contém %q: %v", want, stepActions)
		}
	}
	if !contains(requiredList(p.PlanSchema()), "plan") {
		t.Fatal("PlanSchema deveria exigir o campo plan")
	}
}

func TestInstructionsMencionamAcoes(t *testing.T) {
	for _, name := range Names() {
		s := Get(name)
		if strings.TrimSpace(s.Instructions()) == "" {
			t.Fatalf("%s: instruções vazias", name)
		}
		if s.Schema() == nil {
			t.Fatalf("%s: schema nulo", name)
		}
	}
	if !strings.Contains(Get("cot").Instructions(), "reasoning") {
		t.Fatal("instruções de cot deveriam citar reasoning")
	}
	if !strings.Contains(Get("plan").Instructions(), "replan") {
		t.Fatal("instruções de plan deveriam citar replan")
	}
	if Get("plan").Start() == "" {
		t.Fatal("plan deveria ter instrução de início")
	}
	if Get("react").Start() != "" {
		t.Fatal("react não deveria ter instrução de início")
	}
}
