// Package present formata, em texto legível, o estado que o usuário consulta
// (/history, /sessions e /show): histórico da conversa e sessões persistidas.
//
// O pacote não faz I/O: recebe as mensagens e os registros já lidos e devolve a
// string. Assim ele é testável com dados puros e não conhece o orquestrador —
// a dependência aponta só num sentido (agent → present), sem ciclo.
package present

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
	"agentic-harness/internal/textutil"
)

// History devolve o histórico da conversa em texto legível, omitindo o system
// prompt (que é ruído para o usuário). Histórico sem mensagens visíveis vira
// uma mensagem explícita.
func History(history []llm.Message) string {
	var b strings.Builder
	for _, m := range history {
		if m.Role == llm.RoleSystem {
			continue
		}
		fmt.Fprintf(&b, "[%s] %s\n", m.Role, truncate(m.Content, 300))
	}
	if b.Len() == 0 {
		return "(histórico vazio)"
	}
	return b.String()
}

// Sessions lista as sessões persistidas, da mais recente para a mais antiga.
// path é o arquivo do store (aparece no cabeçalho) e current marca com "*" a
// sessão em uso.
func Sessions(path string, recs []store.Record, current string) string {
	type agg struct {
		turns int
		last  time.Time
	}
	byID := make(map[string]*agg)
	var order []string
	for _, r := range recs {
		if r.Type != "turn" || r.Session == "" {
			continue
		}
		g := byID[r.Session]
		if g == nil {
			g = &agg{}
			byID[r.Session] = g
			order = append(order, r.Session)
		}
		g.turns++
		if r.At.After(g.last) {
			g.last = r.At
		}
	}
	if len(order) == 0 {
		return "(nenhuma sessão persistida)"
	}
	sort.Slice(order, func(i, j int) bool { return byID[order[i]].last.After(byID[order[j]].last) })
	var b strings.Builder
	fmt.Fprintf(&b, "sessões persistidas em %s:\n", path)
	for _, id := range order {
		mark := ""
		if id == current {
			mark = " *"
		}
		fmt.Fprintf(&b, "  %-24s %d turno(s), último %s%s\n", id, byID[id].turns, byID[id].last.Format("2006-01-02 15:04:05"), mark)
	}
	return b.String()
}

// ShowSession mostra os turnos de uma sessão persistida. Só os campos exibidos
// são decodificados do payload — o formato completo do turno é do agent.
func ShowSession(recs []store.Record, id string) string {
	var b strings.Builder
	n := 0
	for _, r := range recs {
		if r.Type != "turn" || r.Session != id {
			continue
		}
		var t struct {
			Input  string `json:"input"`
			Output string `json:"output,omitempty"`
			Error  string `json:"error,omitempty"`
		}
		if err := json.Unmarshal(r.Payload, &t); err != nil {
			continue
		}
		n++
		fmt.Fprintf(&b, "#%d %s\n", n, r.At.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(&b, "  você> %s\n", truncate(t.Input, 300))
		if t.Output != "" {
			fmt.Fprintf(&b, "  assistente> %s\n", truncate(t.Output, 500))
		}
		if t.Error != "" {
			fmt.Fprintf(&b, "  erro: %s\n", t.Error)
		}
	}
	if n == 0 {
		return fmt.Sprintf("(sessão %q não encontrada)", id)
	}
	return b.String()
}

// truncate achata quebras de linha e corta por runas, preservando UTF-8.
func truncate(s string, n int) string {
	return textutil.TruncateRunes(strings.ReplaceAll(s, "\n", " "), n)
}
