// Package memory reúne as operações puras sobre a memória de longo prazo do
// agente: resumo extrativo da conversa, notas persistentes e a injeção desses
// blocos no histórico enviado ao modelo.
//
// Não faz I/O: quem lê e grava no store é o `Agent`. Também não importa o
// pacote `agent` — a seta de dependência é agent -> memory.
package memory

import (
	"math"
	"strings"

	"agentic-harness/internal/llm"
	"agentic-harness/internal/textutil"
)

const (
	// Prefix abre o bloco do resumo acumulado da conversa.
	Prefix = "MEMÓRIA DA CONVERSA (resumo acumulado):\n"
	// NotesPrefix abre o bloco das notas persistentes.
	NotesPrefix = "FATOS LEMBRADOS (memória de longo prazo):\n"

	// MaxNotes limita quantas linhas/notas ficam no contexto.
	MaxNotes = 40
	// MaxChars limita o tamanho total do resumo acumulado.
	MaxChars = 8000
	// MaxNoteChars limita o tamanho de uma nota individual.
	MaxNoteChars = 500

	// lineChars limita o tamanho de uma linha do resumo.
	lineChars = 200
)

// Line resume uma mensagem descartada em uma linha curta do resumo. Devolve ""
// para mensagens que não valem a pena resumir (system, vazias).
func Line(m llm.Message) string {
	content := strings.TrimSpace(strings.ReplaceAll(m.Content, "\n", " "))
	if content == "" {
		return ""
	}
	var role string
	switch {
	case strings.HasPrefix(content, "OBSERVAÇÃO (") || strings.HasPrefix(content, "ERRO ("):
		role = "harness"
	case m.Role == llm.RoleUser:
		role = "você"
	case m.Role == llm.RoleAssistant:
		role = "assistente"
	default:
		return ""
	}
	return "- " + role + ": " + textutil.TruncateRunes(content, lineChars)
}

// Summarize acumula no resumo as linhas das mensagens descartadas pelo
// orçamento de contexto. Linhas repetidas em sequência são ignoradas; o resumo
// respeita MaxNotes (linhas) e MaxChars (tamanho total).
func Summarize(summary []string, dropped []llm.Message) []string {
	out := summary
	for _, m := range dropped {
		line := Line(m)
		if line == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1] == line {
			continue
		}
		out = append(out, line)
	}
	if len(out) > MaxNotes {
		out = out[len(out)-MaxNotes:]
	}
	for len(out) > 1 && len(strings.Join(out, "\n")) > MaxChars {
		out = out[1:]
	}
	return out
}

// Normalize limpa uma nota: uma linha só, sem espaços sobrando e limitada a
// MaxNoteChars. ok=false quando a nota fica vazia.
func Normalize(note string) (clean string, ok bool) {
	clean = strings.TrimSpace(strings.ReplaceAll(note, "\n", " "))
	if clean == "" {
		return "", false
	}
	if len(clean) > MaxNoteChars {
		clean = textutil.TruncateRunes(clean, MaxNoteChars)
	}
	return clean, true
}

// Merge devolve as notas conhecidas acrescidas das persistidas, sem duplicar
// (a primeira ocorrência vence) e mantendo no máximo MaxNotes.
func Merge(known, persisted []string) []string {
	out := make([]string, 0, len(known)+len(persisted))
	seen := make(map[string]bool, len(known)+len(persisted))
	for _, list := range [][]string{known, persisted} {
		for _, n := range list {
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > MaxNotes {
		out = out[len(out)-MaxNotes:]
	}
	return out
}

// Cosine calcula a similaridade de cosseno entre dois vetores. Devolve 0 para
// vetores vazios, de tamanhos diferentes ou nulos.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// Is informa se a mensagem é um dos blocos de memória injetados por este pacote.
func Is(m llm.Message) bool {
	return strings.HasPrefix(m.Content, Prefix) || strings.HasPrefix(m.Content, NotesPrefix)
}

// Render devolve as mensagens de memória a injetar no histórico, na ordem:
// notas persistentes e depois resumo. Blocos vazios são omitidos.
func Render(notes, summary []string) []llm.Message {
	var out []llm.Message
	if len(notes) > 0 {
		var b strings.Builder
		b.WriteString(NotesPrefix)
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: strings.TrimRight(b.String(), "\n")})
	}
	if len(summary) > 0 {
		var b strings.Builder
		b.WriteString(Prefix)
		for _, m := range summary {
			b.WriteString(m + "\n")
		}
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: strings.TrimRight(b.String(), "\n")})
	}
	return out
}

// Inject remove os blocos de memória antigos do histórico e insere os atuais
// logo após o system prompt, devolvendo o histórico resultante.
func Inject(history []llm.Message, notes, summary []string) []llm.Message {
	kept := make([]llm.Message, 0, len(history))
	for _, m := range history {
		if Is(m) {
			continue
		}
		kept = append(kept, m)
	}
	extra := Render(notes, summary)
	pos := 0
	if len(kept) > 0 && kept[0].Role == llm.RoleSystem {
		pos = 1
	}
	out := make([]llm.Message, 0, len(kept)+len(extra))
	out = append(out, kept[:pos]...)
	out = append(out, extra...)
	out = append(out, kept[pos:]...)
	return out
}
