package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"agentic-harness/internal/llm"
	"agentic-harness/internal/store"
	"agentic-harness/internal/textutil"
)

// Memória de longo prazo do agente, em duas camadas:
//
//  1. resumo acumulado (extrativo) das mensagens descartadas pelo orçamento de
//     contexto (-maxcontext): mantém o fio da conversa em sessões longas sem
//     chamadas extras ao modelo;
//  2. notas persistentes no store (registros do tipo "memory"): fatos que o
//     usuário (ou o próprio agente, via tool "remember") grava e que valem
//     entre sessões.
const (
	memoryPrefix = "MEMÓRIA DA CONVERSA (resumo acumulado):\n"
	notesPrefix  = "FATOS LEMBRADOS (memória de longo prazo):\n"

	// maxMemoryNotes limita quantas linhas/notas ficam no contexto.
	maxMemoryNotes = 40
	// maxMemoryChars limita o tamanho total do resumo acumulado.
	maxMemoryChars = 8000
	// maxNoteChars limita o tamanho de uma nota individual.
	maxNoteChars = 500
)

// noteRecord é o payload persistido para uma nota de memória.
type noteRecord struct {
	Text string `json:"text"`
}

// Embedder é o subconjunto do cliente usado pela busca por similaridade nas
// notas de memória (RAG leve).
type Embedder interface {
	Embed(ctx context.Context, req llm.EmbeddingRequest) ([][]float32, error)
}

// SetEmbedder liga a busca por similaridade nas notas (-rag). Sem embedder (ou
// com o RAG desligado), todas as notas são injetadas no contexto.
func (a *Agent) SetEmbedder(e Embedder) { a.embed = e }

// notesForContext devolve as notas a injetar no contexto: as escolhidas por
// similaridade no turno atual ou, na ausência de seleção, todas.
func (a *Agent) notesForContext() []string {
	if len(a.selected) > 0 {
		return a.selected
	}
	return a.notes
}

// retrieveNotes seleciona as notas mais relevantes para a mensagem atual usando
// embeddings (top-k por similaridade de cosseno). Qualquer falha — RAG
// desligado, sem embedder, poucas notas ou erro no /embeddings — cai no
// comportamento seguro de injetar todas as notas.
func (a *Agent) retrieveNotes(ctx context.Context, query string) {
	a.selected = nil
	top := a.cfg.TopK
	if !a.cfg.RAG || a.embed == nil || len(a.notes) <= top {
		a.refreshMemory()
		return
	}
	if top <= 0 {
		top = 5
	}
	queryVec, err := a.embedOne(ctx, query)
	if err != nil {
		a.logf("rag: embedding da consulta falhou: %v", err)
		a.refreshMemory()
		return
	}
	type scored struct {
		note  string
		score float64
	}
	ranked := make([]scored, 0, len(a.notes))
	for _, n := range a.notes {
		noteVec, err := a.embedOne(ctx, n)
		if err != nil {
			a.logf("rag: embedding da nota falhou: %v", err)
			a.refreshMemory()
			return
		}
		ranked = append(ranked, scored{note: n, score: cosineSimilarity(queryVec, noteVec)})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if top > len(ranked) {
		top = len(ranked)
	}
	selected := make([]string, 0, top)
	for _, r := range ranked[:top] {
		selected = append(selected, r.note)
	}
	a.selected = selected
	a.logf("rag: %d de %d nota(s) no contexto", len(selected), len(a.notes))
	a.refreshMemory()
}

// embedOne gera (e memoriza) o embedding de um texto.
func (a *Agent) embedOne(ctx context.Context, text string) ([]float32, error) {
	if v, ok := a.noteVecs[text]; ok && len(v) > 0 {
		return v, nil
	}
	vecs, err := a.embed.Embed(ctx, llm.EmbeddingRequest{Model: a.cfg.EmbedModel, Input: text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 || len(vecs[0]) == 0 {
		return nil, fmt.Errorf("embedding vazio")
	}
	if a.noteVecs == nil {
		a.noteVecs = make(map[string][]float32)
	}
	a.noteVecs[text] = vecs[0]
	return vecs[0], nil
}

// cosineSimilarity calcula a similaridade de cosseno entre dois vetores.
func cosineSimilarity(a, b []float32) float64 {
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

// remember acumula no resumo as mensagens descartadas pelo trimHistory.
func (a *Agent) remember(dropped []llm.Message) {
	for _, m := range dropped {
		line := memoryLine(m)
		if line == "" {
			continue
		}
		if n := len(a.memory); n > 0 && a.memory[n-1] == line {
			continue
		}
		a.memory = append(a.memory, line)
	}
	if len(a.memory) > maxMemoryNotes {
		a.memory = a.memory[len(a.memory)-maxMemoryNotes:]
	}
	for len(a.memory) > 1 && len(strings.Join(a.memory, "\n")) > maxMemoryChars {
		a.memory = a.memory[1:]
	}
	a.refreshMemory()
}

// memoryLine resume uma mensagem descartada em uma linha curta.
func memoryLine(m llm.Message) string {
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
	return "- " + role + ": " + truncateForLog(content, 200)
}

// refreshMemory (re)injeta as mensagens de memória logo após o system prompt,
// removendo versões antigas antes de inserir as atuais.
func (a *Agent) refreshMemory() {
	kept := a.history[:0]
	for _, m := range a.history {
		if strings.HasPrefix(m.Content, memoryPrefix) || strings.HasPrefix(m.Content, notesPrefix) {
			continue
		}
		kept = append(kept, m)
	}
	a.history = kept

	var extra []llm.Message
	if notes := a.notesForContext(); len(notes) > 0 {
		var b strings.Builder
		b.WriteString(notesPrefix)
		for _, n := range notes {
			b.WriteString("- " + n + "\n")
		}
		extra = append(extra, llm.Message{Role: llm.RoleSystem, Content: strings.TrimRight(b.String(), "\n")})
	}
	if len(a.memory) > 0 {
		var b strings.Builder
		b.WriteString(memoryPrefix)
		for _, m := range a.memory {
			b.WriteString(m + "\n")
		}
		extra = append(extra, llm.Message{Role: llm.RoleSystem, Content: strings.TrimRight(b.String(), "\n")})
	}
	if len(extra) == 0 {
		return
	}
	pos := 0
	if len(a.history) > 0 && a.history[0].Role == llm.RoleSystem {
		pos = 1
	}
	out := make([]llm.Message, 0, len(a.history)+len(extra))
	out = append(out, a.history[:pos]...)
	out = append(out, extra...)
	out = append(out, a.history[pos:]...)
	a.history = out
}

// Remember grava uma nota de memória de longo prazo. Mantém em memória para a
// sessão atual e persiste no store quando houver um conectado.
func (a *Agent) Remember(note string) error {
	note = strings.TrimSpace(strings.ReplaceAll(note, "\n", " "))
	if note == "" {
		return fmt.Errorf("nota vazia")
	}
	if len(note) > maxNoteChars {
		note = textutil.TruncateRunes(note, maxNoteChars)
	}
	a.notes = append(a.notes, note)
	if len(a.notes) > maxMemoryNotes {
		a.notes = a.notes[len(a.notes)-maxMemoryNotes:]
	}
	// Uma nota nova invalida a seleção por similaridade do turno anterior.
	a.selected = nil
	a.refreshMemory()
	if a.store == nil {
		return nil
	}
	return a.store.Append("memory", a.session, noteRecord{Text: note})
}

// Forget limpa o resumo acumulado e as notas da sessão atual. O store não é
// reescrito: sessões já persistidas continuam intactas.
func (a *Agent) Forget() {
	a.memory = nil
	a.notes = nil
	a.selected = nil
	a.refreshMemory()
}

// Memory devolve o estado da memória de longo prazo em texto legível.
func (a *Agent) Memory() string {
	var b strings.Builder
	b.WriteString("notas persistentes:\n")
	if len(a.notes) == 0 {
		b.WriteString("  (nenhuma)\n")
	}
	inContext := make(map[string]bool, len(a.selected))
	for _, n := range a.selected {
		inContext[n] = true
	}
	rag := a.cfg.RAG && a.embed != nil
	for _, n := range a.notes {
		mark := ""
		if rag && inContext[n] {
			mark = "  [no contexto]"
		}
		b.WriteString("  - " + n + mark + "\n")
	}
	if rag {
		fmt.Fprintf(&b, "  (RAG ativo: %d de %d nota(s) no contexto atual)\n", len(a.selected), len(a.notes))
	}
	b.WriteString("resumo da conversa:\n")
	if len(a.memory) == 0 {
		b.WriteString("  (vazio)\n")
	}
	for _, m := range a.memory {
		b.WriteString("  " + m + "\n")
	}
	return b.String()
}

// LoadMemory carrega as notas persistentes do store no contexto atual.
func (a *Agent) LoadMemory() error {
	if a.store == nil {
		return nil
	}
	recs, err := a.store.Query()
	if err != nil {
		return err
	}
	a.applyNotes(recs)
	a.selected = nil
	a.refreshMemory()
	return nil
}

// applyNotes acrescenta as notas persistidas, sem duplicar as já conhecidas.
func (a *Agent) applyNotes(recs []store.Record) {
	seen := make(map[string]bool, len(a.notes))
	for _, n := range a.notes {
		seen[n] = true
	}
	for _, r := range recs {
		if r.Type != "memory" {
			continue
		}
		var n noteRecord
		if err := json.Unmarshal(r.Payload, &n); err != nil {
			continue
		}
		text := strings.TrimSpace(n.Text)
		if text == "" || seen[text] {
			continue
		}
		seen[text] = true
		a.notes = append(a.notes, text)
	}
	if len(a.notes) > maxMemoryNotes {
		a.notes = a.notes[len(a.notes)-maxMemoryNotes:]
	}
}
