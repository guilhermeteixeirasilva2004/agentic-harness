package ui

import (
	"context"
	"strings"

	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
)

// Chat é o Engine de conversa livre, com streaming token a token.
type Chat struct {
	cfg     *config.Config
	client  *llm.Client
	history []llm.Message
}

// NewChat cria um engine de chat simples.
func NewChat(cfg *config.Config, client *llm.Client) *Chat {
	c := &Chat{cfg: cfg, client: client}
	c.rebuildHistory()
	return c
}

func (c *Chat) rebuildHistory() {
	c.history = c.history[:0]
	if strings.TrimSpace(c.cfg.System) != "" {
		c.history = append(c.history, llm.Message{Role: llm.RoleSystem, Content: c.cfg.System})
	}
}

// Reply envia a mensagem e faz streaming da resposta.
func (c *Chat) Reply(ctx context.Context, input string, onDelta func(string)) (string, error) {
	c.history = append(c.history, llm.Message{Role: llm.RoleUser, Content: input})
	reply, err := c.client.Chat(ctx, llm.ChatRequest{
		Model:       c.cfg.Model,
		Messages:    c.history,
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
	}, onDelta)
	if err != nil {
		// desfaz a mensagem do usuário que falhou para não corromper o histórico
		c.history = c.history[:len(c.history)-1]
		return "", err
	}
	c.history = append(c.history, llm.Message{Role: llm.RoleAssistant, Content: reply})
	return reply, nil
}

// SetSystem define (ou limpa, se vazio) o system prompt, preservando o restante.
func (c *Chat) SetSystem(system string) {
	c.cfg.System = system
	rest := make([]llm.Message, 0, len(c.history))
	for _, m := range c.history {
		if m.Role != llm.RoleSystem {
			rest = append(rest, m)
		}
	}
	c.history = c.history[:0]
	if strings.TrimSpace(system) != "" {
		c.history = append(c.history, llm.Message{Role: llm.RoleSystem, Content: system})
	}
	c.history = append(c.history, rest...)
}

// Clear reinicia o histórico.
func (c *Chat) Clear() { c.rebuildHistory() }
