package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// rememberTool permite ao modelo gravar um fato na memória de longo prazo
// (persistente entre sessões, no store NDJSON).
type rememberTool struct{ a *Agent }

func (rememberTool) Name() string { return "remember" }
func (rememberTool) Description() string {
	return "Grava um fato relevante (preferência, decisão, detalhe do projeto) na memória de longo prazo."
}

func (rememberTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"note": map[string]any{"type": "string", "description": "fato curto a lembrar nas próximas conversas"},
		},
		"required": []string{"note"},
	}
}

func (t rememberTool) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Note string `json:"note"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	if strings.TrimSpace(in.Note) == "" {
		return "", fmt.Errorf(`argumento "note" é obrigatório`)
	}
	if err := t.a.Remember(in.Note); err != nil {
		return "", err
	}
	return "nota gravada na memória de longo prazo", nil
}
