package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Asker é o canal de interação com o humano usado pela tool ask_user.
type Asker interface {
	// Ask faz uma pergunta no terminal e devolve a resposta do usuário.
	Ask(ctx context.Context, question string) (string, error)
}

// SetAsker define quem responde às perguntas da tool ask_user. Sem asker, a
// tool falha com uma mensagem clara (execuções não interativas).
func (a *Agent) SetAsker(as Asker) { a.asker = as }

// askUserTool permite ao agente perguntar algo ao usuário e esperar a resposta.
// Não exige aprovação: apenas obtém informação, sem alterar o sistema.
type askUserTool struct{ a *Agent }

func (askUserTool) Name() string { return "ask_user" }

func (askUserTool) Description() string {
	return "Pergunta algo ao usuário e espera a resposta; use quando faltar uma informação que só o usuário pode fornecer."
}

func (askUserTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{"type": "string", "description": "pergunta curta e objetiva"},
		},
		"required": []string{"question"},
	}
}

func (t askUserTool) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Question string `json:"question"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	question := strings.TrimSpace(in.Question)
	if question == "" {
		return "", fmt.Errorf(`argumento "question" é obrigatório`)
	}
	if t.a.asker == nil {
		return "", fmt.Errorf("sem canal de interação com o usuário")
	}
	answer, err := t.a.asker.Ask(ctx, question)
	if err != nil {
		return "", fmt.Errorf("pergunta não respondida: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return "o usuário não respondeu", nil
	}
	return "resposta do usuário: " + truncateForLog(answer, 1000), nil
}
