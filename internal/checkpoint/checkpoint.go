// Package checkpoint carrega o "save state" da tarefa em andamento: o estado
// gravado quando os passos acabam sem resposta final, para retomar em outro
// processo (-continue), onde a memória do processo já não existe.
//
// O pacote é puro: recebe os registros já lidos do store e devolve o checkpoint
// pendente. Gravar, restaurar e dirigir o loop continuam no agent.
package checkpoint

import (
	"encoding/json"
	"strings"

	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
)

// ContinuePrompt é a instrução enviada ao retomar uma tarefa incompleta. O
// histórico (com as observações já coletadas) é restaurado antes, então o
// agente prossegue sem repetir o trabalho feito.
const ContinuePrompt = "Continue a tarefa anterior de onde parou, sem repetir o que já foi feito. " +
	"Use as observações já disponíveis no histórico e conclua com action \"final\" assim que possível."

// Record é o estado da tarefa em andamento: histórico completo (incluindo
// observações), memória, notas, plano e estratégia.
type Record struct {
	Input    string        `json:"input"`
	History  []llm.Message `json:"history"`
	Memory   []string      `json:"memory,omitempty"`
	Notes    []string      `json:"notes,omitempty"`
	Plan     []string      `json:"plan,omitempty"`
	Strategy string        `json:"strategy"`
	Model    string        `json:"model"`
}

// Pending devolve o checkpoint mais recente ainda não concluído por um turno
// bem-sucedido. explicit indica sessão fixada; sem isso, usa o último
// checkpoint gravado (qualquer sessão). Um turno que terminou com erro não
// consome o checkpoint (a tarefa segue incompleta).
func Pending(recs []store.Record, session string, explicit bool) (Record, string, bool) {
	idx := -1
	var cp Record
	var cpSession string
	for i, r := range recs {
		switch r.Type {
		case "checkpoint":
			if explicit && r.Session != session {
				continue
			}
			var c Record
			if err := json.Unmarshal(r.Payload, &c); err != nil {
				continue
			}
			cp, cpSession, idx = c, r.Session, i
		case "turn":
			if idx < 0 || r.Session != cpSession {
				continue
			}
			// Só o campo que decide se o turno concluiu importa aqui; o
			// restante do payload é do agent.
			var t struct {
				Error string `json:"error,omitempty"`
			}
			if err := json.Unmarshal(r.Payload, &t); err == nil && strings.TrimSpace(t.Error) == "" {
				// Conclusão bem-sucedida depois do checkpoint.
				idx = -1
			}
		}
	}
	if idx < 0 {
		return Record{}, "", false
	}
	return cp, cpSession, true
}
