// Package cognition modela a cognição do agente: um trace estruturado de
// pensamentos, plano, decisões, chamadas de tool e observações. É puro (só
// stdlib), de modo que o loop em internal/agent e os testes possam reutilizá-lo
// sem depender do cliente LLM.
package cognition

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind classifica uma etapa do trace.
type Kind string

const (
	KindThought     Kind = "thought"
	KindPlan        Kind = "plan"
	KindDecision    Kind = "decision"
	KindToolCall    Kind = "tool_call"
	KindObservation Kind = "observation"
	KindFinal       Kind = "final"
	KindError       Kind = "error"
)

// Step é uma etapa do raciocínio. Nem todos os campos se aplicam a todos os
// Kind: Reasoning aparece em qualquer etapa, Plan só em KindPlan, e assim por
// diante.
type Step struct {
	N           int             `json:"n"`
	Kind        Kind            `json:"kind"`
	Reasoning   string          `json:"reasoning,omitempty"`
	Plan        []string        `json:"plan,omitempty"`
	Action      string          `json:"action,omitempty"`
	Tool        string          `json:"tool,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
	Observation string          `json:"observation,omitempty"`
	Answer      string          `json:"answer,omitempty"`
	Tokens      int             `json:"tokens,omitempty"`
	At          time.Time       `json:"at"`
}

// text reúne os campos textuais de uma etapa para estimar tokens.
func (s Step) text() string {
	var b strings.Builder
	for _, p := range []string{s.Reasoning, s.Answer, s.Observation, strings.Join(s.Plan, "\n"), string(s.Arguments)} {
		if p == "" {
			continue
		}
		b.WriteString(p)
		b.WriteString("\n")
	}
	return b.String()
}

// Trace é a sequência ordenada de etapas de uma conversa.
type Trace struct {
	ID    string `json:"id"`
	Steps []Step `json:"steps"`
}

// New cria um trace vazio com o identificador informado.
func New(id string) *Trace { return &Trace{ID: id} }

// Append acrescenta uma etapa, preenchendo N, At e Tokens quando ausentes.
func (t *Trace) Append(s Step) Step {
	s.N = len(t.Steps) + 1
	if s.At.IsZero() {
		s.At = time.Now()
	}
	if s.Tokens == 0 {
		s.Tokens = EstimateTokens(s.text())
	}
	t.Steps = append(t.Steps, s)
	return s
}

// Len devolve o número de etapas registradas.
func (t *Trace) Len() int { return len(t.Steps) }

// Last devolve as n etapas mais recentes (todas, se n <= 0).
func (t *Trace) Last(n int) []Step {
	if n <= 0 || n > len(t.Steps) {
		n = len(t.Steps)
	}
	if n == 0 {
		return nil
	}
	out := make([]Step, n)
	copy(out, t.Steps[len(t.Steps)-n:])
	return out
}

// Plan devolve o plano mais recente registrado no trace.
func (t *Trace) Plan() []string {
	for i := len(t.Steps) - 1; i >= 0; i-- {
		if t.Steps[i].Kind == KindPlan {
			return t.Steps[i].Plan
		}
	}
	return nil
}

// Tokens soma a estimativa de tokens de todas as etapas.
func (t *Trace) Tokens() int {
	total := 0
	for _, s := range t.Steps {
		total += s.Tokens
	}
	return total
}

// Compact reduz o trace para caber no orçamento de tokens. As etapas mais
// antigas perdem primeiro o raciocínio, a observação e os argumentos; as
// respostas finais e as keepLast etapas mais recentes são preservadas.
func (t *Trace) Compact(maxTokens, keepLast int) {
	if maxTokens <= 0 {
		return
	}
	if keepLast <= 0 {
		keepLast = 5
	}
	if t.Tokens() <= maxTokens {
		return
	}
	cutoff := len(t.Steps) - keepLast
	for i := 0; i < cutoff && t.Tokens() > maxTokens; i++ {
		s := &t.Steps[i]
		if s.Kind == KindFinal {
			continue
		}
		s.Reasoning = ""
		s.Observation = ""
		s.Arguments = nil
		s.Tokens = EstimateTokens(s.text())
	}
}

// RenderMarkdown descreve o trace em texto legível (usado por -verbose e /trace).
func (t *Trace) RenderMarkdown() string {
	if t.Len() == 0 {
		return "(trace vazio)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "trace %s (%d etapas, ~%d tokens)\n", t.ID, t.Len(), t.Tokens())
	for _, s := range t.Steps {
		fmt.Fprintf(&b, "%2d. %s", s.N, s.Kind)
		switch s.Kind {
		case KindPlan:
			if len(s.Plan) > 0 {
				b.WriteString(": ")
				b.WriteString(strings.Join(s.Plan, " | "))
			}
		case KindToolCall:
			fmt.Fprintf(&b, ": %s", s.Tool)
			if args := oneLine(string(s.Arguments), 120); args != "" {
				b.WriteString(" ")
				b.WriteString(args)
			}
		case KindObservation, KindError:
			if s.Tool != "" {
				fmt.Fprintf(&b, " (%s)", s.Tool)
			}
			if s.Observation != "" {
				b.WriteString(": ")
				b.WriteString(oneLine(s.Observation, 200))
			}
		case KindFinal:
			if s.Answer != "" {
				b.WriteString(": ")
				b.WriteString(oneLine(s.Answer, 200))
			}
		}
		b.WriteString("\n")
		if s.Reasoning != "" {
			fmt.Fprintf(&b, "     pensamento: %s\n", oneLine(s.Reasoning, 240))
		}
	}
	return b.String()
}

// EstimateTokens estima o custo de um texto em tokens (~4 caracteres por token).
func EstimateTokens(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return (utf8.RuneCountInString(s) + 3) / 4
}

// Budget devolve quantos itens iniciais devem ser descartados para que a soma
// dos pesos caiba em maxTokens, sempre preservando os itens mais recentes.
// maxTokens <= 0 desativa o orçamento.
func Budget(weights []int, maxTokens int) int {
	if maxTokens <= 0 {
		return 0
	}
	total := 0
	for _, w := range weights {
		total += w
	}
	if total <= maxTokens {
		return 0
	}
	for i := 0; i < len(weights); i++ {
		total -= weights[i]
		if total <= maxTokens {
			return i + 1
		}
	}
	return len(weights)
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}
