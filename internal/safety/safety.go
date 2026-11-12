// Package safety concentra os guardrails do agente: limites de tamanho,
// allowlist de tools e teto de passos. É deliberadamente conservador: por
// padrão nega toda tool que não esteja explicitamente liberada.
package safety

import (
	"fmt"
	"strings"

	"agentic-harness/internal/textutil"
)

// Policy define os limites aplicados a cada rodada do agente.
type Policy struct {
	MaxInputBytes  int
	MaxOutputBytes int
	MaxSteps       int
	Allow          map[string]bool
}

// New cria uma política com limites padrão e a allowlist informada.
func New(maxSteps int, allow []string) *Policy {
	if maxSteps <= 0 {
		maxSteps = 4
	}
	p := &Policy{
		MaxInputBytes:  32 * 1024,
		MaxOutputBytes: 64 * 1024,
		MaxSteps:       maxSteps,
		Allow:          make(map[string]bool, len(allow)),
	}
	for _, n := range allow {
		if n = strings.TrimSpace(n); n != "" {
			p.Allow[n] = true
		}
	}
	return p
}

// CheckInput recusa entradas acima do limite.
func (p *Policy) CheckInput(s string) error {
	if len(s) > p.MaxInputBytes {
		return fmt.Errorf("entrada excede o limite de %d bytes", p.MaxInputBytes)
	}
	return nil
}

// AllowTool informa se a tool está liberada (nega por padrão).
func (p *Policy) AllowTool(name string) bool { return p.Allow[name] }

// CheckTool recusa tools fora da allowlist.
func (p *Policy) CheckTool(name string) error {
	if !p.AllowTool(name) {
		return fmt.Errorf("tool bloqueada pela política: %s", name)
	}
	return nil
}

// TruncateOutput limita a resposta final ao teto de bytes. O corte respeita
// UTF-8 e o aviso de truncamento cabe dentro do teto: o texto devolvido nunca
// passa de MaxOutputBytes (exceto quando o teto é menor que o próprio aviso).
func (p *Policy) TruncateOutput(s string) string {
	const notice = "\n[... resposta truncada pela política]"
	if p.MaxOutputBytes <= 0 || len(s) <= p.MaxOutputBytes {
		return s
	}
	limit := p.MaxOutputBytes - len(notice)
	if limit <= 0 {
		return textutil.CutBytesUTF8(s, p.MaxOutputBytes)
	}
	return textutil.CutBytesUTF8(s, limit) + notice
}

// StepsOK informa se ainda há orçamento de passos.
func (p *Policy) StepsOK(step int) bool { return step < p.MaxSteps }
