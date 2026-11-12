// Package reasoning define as estratégias de raciocínio do agente. Cada
// estratégia molda o prompt e o JSON Schema da decisão devolvida pelo modelo; o
// loop (pensar → agir → observar) continua em internal/agent.
//
// Estratégias disponíveis:
//   - react (padrão): decide entre tool e final, sem raciocínio explícito.
//   - cot: exige uma cadeia de pensamento ("reasoning") a cada passo.
//   - plan: primeiro passo gera um plano; é possível replanejar no meio.
package reasoning

import (
	"encoding/json"
	"strings"
)

// Decision é a decisão estruturada devolvida pelo modelo a cada passo.
type Decision struct {
	Action    string          `json:"action"`
	Tool      string          `json:"tool,omitempty"`
	Reasoning string          `json:"reasoning,omitempty"`
	Plan      []string        `json:"plan,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Answer    string          `json:"answer,omitempty"`
}

// Strategy descreve como o modelo deve estruturar cada decisão.
type Strategy interface {
	// Name é o identificador usado pela flag -strategy.
	Name() string
	// Instructions documenta as ações aceitas (vai no system prompt).
	Instructions() string
	// Schema devolve o JSON Schema da decisão.
	Schema() map[string]any
	// Start devolve uma instrução extra para o primeiro passo (vazio se não há).
	Start() string
}

// Planner é implementado por estratégias com plano persistente (plan-and-execute).
// O schema muda conforme já exista um plano: o primeiro passo só propõe o plano;
// os seguintes executam, revisam ou concluem.
type Planner interface {
	Strategy
	// PlanSchema é o schema do primeiro passo, que só pode propor um plano.
	PlanSchema() map[string]any
	// StepSchema é o schema dos passos seguintes (tool, replan ou final).
	StepSchema() map[string]any
}

// Get devolve a estratégia pelo nome (case-insensitive). Nomes desconhecidos
// caem em react, preservando o comportamento padrão.
func Get(name string) Strategy {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cot", "chain-of-thought", "chain_of_thought":
		return cot{}
	case "plan", "plan-execute", "plan_execute":
		return plan{}
	default:
		return react{}
	}
}

// Names lista as estratégias disponíveis em ordem estável.
func Names() []string { return []string{"react", "cot", "plan"} }

// react é o comportamento clássico: só a decisão, sem raciocínio explícito.
type react struct{}

func (react) Name() string { return "react" }

func (react) Instructions() string {
	return "- para agir:     {\"action\":\"tool\",\"tool\":\"<nome>\"}\n" +
		"- para concluir: {\"action\":\"final\",\"answer\":\"<resposta ao usuário>\"}"
}

func (react) Schema() map[string]any { return buildSchema([]string{"tool", "final"}, false, false) }

func (react) Start() string { return "" }

// cot exige cadeia de pensamento: o campo reasoning aparece em toda decisão.
type cot struct{}

func (cot) Name() string { return "cot" }

func (cot) Instructions() string {
	return "- para agir:     {\"action\":\"tool\",\"tool\":\"<nome>\",\"reasoning\":\"<por que essa tool>\"}\n" +
		"- para concluir: {\"action\":\"final\",\"answer\":\"<resposta ao usuário>\",\"reasoning\":\"<como chegou>\"}"
}

func (cot) Schema() map[string]any { return buildSchema([]string{"tool", "final"}, true, false) }

func (cot) Start() string {
	return "Antes de agir, explique brevemente seu raciocínio no campo reasoning."
}

// plan implementa plan-and-execute leve: plano explícito no início e replan.
type plan struct{}

func (plan) Name() string { return "plan" }

func (plan) Instructions() string {
	return "- primeiro passo: {\"action\":\"plan\",\"plan\":[\"<etapa 1>\",\"<etapa 2>\"],\"reasoning\":\"<estratégia>\"}\n" +
		"  As etapas devem ser ações concretas em linguagem natural (ex.: \"ler o go.mod\"), não nomes de tools.\n" +
		"- para agir:      {\"action\":\"tool\",\"tool\":\"<nome>\",\"reasoning\":\"<etapa atual do plano>\"}\n" +
		"- para revisar:   {\"action\":\"replan\",\"plan\":[\"<novo plano>\"],\"reasoning\":\"<por que mudou>\"}\n" +
		"- para concluir:  {\"action\":\"final\",\"answer\":\"<resposta ao usuário>\",\"reasoning\":\"<como chegou>\"}"
}

func (plan) Schema() map[string]any {
	return buildSchema([]string{"plan", "tool", "replan", "final"}, true, true)
}

// PlanSchema força o primeiro passo a propor um plano.
func (plan) PlanSchema() map[string]any {
	s := buildSchema([]string{"plan"}, true, true)
	s["required"] = []string{"action", "plan", "reasoning"}
	return s
}

// StepSchema impede novos planos depois que já existe um: resta executar
// (tool), revisar (replan) ou concluir (final).
func (plan) StepSchema() map[string]any {
	s := buildSchema([]string{"tool", "replan", "final"}, true, true)
	s["required"] = []string{"action", "tool", "reasoning"}
	return s
}

func (plan) Start() string {
	return "Comece propondo um plano com action \"plan\" e só então execute cada etapa."
}

// buildSchema monta o schema da decisão com os campos opcionais da estratégia.
func buildSchema(actions []string, withReasoning, withPlan bool) map[string]any {
	props := map[string]any{
		"action": map[string]any{"type": "string", "enum": actions},
		"tool":   map[string]any{"type": "string"},
		"answer": map[string]any{"type": "string"},
	}
	required := []string{"action", "tool"}
	if withReasoning {
		props["reasoning"] = map[string]any{"type": "string"}
		required = append(required, "reasoning")
	}
	if withPlan {
		props["plan"] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}
