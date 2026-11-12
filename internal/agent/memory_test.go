package agent

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/store"
	"agentic-harness/internal/tools"
)

// newMemoryAgent cria um agente mínimo (sem tools mutantes) para testes de memória.
func newMemoryAgent() *Agent {
	cfg := &config.Config{System: "teste", MaxSteps: 4}
	return New(cfg, &fakeLLM{}, tools.NewRegistry(), safety.New(4, nil))
}

func TestTrimHistoryResumeNaMemoria(t *testing.T) {
	cfg := &config.Config{System: "s", MaxSteps: 4, MaxContextTokens: 10}
	a := New(cfg, &fakeLLM{}, tools.NewRegistry(), safety.New(4, nil))
	a.history = append(a.history,
		llm.Message{Role: llm.RoleUser, Content: strings.Repeat("u", 400)},
		llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 400)},
		llm.Message{Role: llm.RoleUser, Content: "OBSERVAÇÃO (read_file): conteúdo"},
		llm.Message{Role: llm.RoleUser, Content: "pergunta atual"},
	)

	a.trimHistory()

	mem := a.history[1].Content
	for _, want := range []string{"você: ", "assistente: ", "harness: OBSERVAÇÃO"} {
		if !strings.Contains(mem, want) {
			t.Fatalf("resumo sem %q: %q", want, mem)
		}
	}
	if !strings.Contains(a.Memory(), "resumo da conversa") {
		t.Fatalf("Memory() inesperado: %q", a.Memory())
	}
	// Um segundo trim não deve duplicar o resumo no histórico.
	a.history = append(a.history, llm.Message{Role: llm.RoleUser, Content: strings.Repeat("z", 400)})
	a.trimHistory()
	count := 0
	for _, m := range a.history {
		if strings.HasPrefix(m.Content, memoryPrefix) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("resumo duplicado no histórico (%d ocorrências): %+v", count, a.history)
	}
}

func TestRememberPersisteNoStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := newMemoryAgent()
	a.SetStore(st)
	a.SetSession("s1")
	if err := a.Remember("o usuário prefere respostas curtas"); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(a.history[1].Content, "respostas curtas") {
		t.Fatalf("nota não foi injetada no contexto: %+v", a.history)
	}
	if !strings.Contains(a.Memory(), "respostas curtas") {
		t.Fatalf("Memory() não mostra a nota: %q", a.Memory())
	}

	// Uma nova instância carrega a nota persistida do store.
	b := newMemoryAgent()
	b.SetStore(st)
	if err := b.LoadMemory(); err != nil {
		t.Fatalf("LoadMemory: %v", err)
	}
	if !strings.Contains(b.Memory(), "respostas curtas") {
		t.Fatalf("nota não foi carregada do store: %q", b.Memory())
	}

	// Carregar duas vezes não deve duplicar a nota.
	if err := b.LoadMemory(); err != nil {
		t.Fatalf("LoadMemory: %v", err)
	}
	if n := strings.Count(b.Memory(), "respostas curtas"); n != 1 {
		t.Fatalf("nota duplicada (%d ocorrências): %q", n, b.Memory())
	}
}

func TestForgetLimpaMemoriaDaSessao(t *testing.T) {
	a := newMemoryAgent()
	if err := a.Remember("fato qualquer"); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	a.Forget()

	if len(a.notes) != 0 || len(a.memory) != 0 {
		t.Fatalf("memória não foi limpa: notes=%v memory=%v", a.notes, a.memory)
	}
	for _, m := range a.history {
		if strings.HasPrefix(m.Content, notesPrefix) || strings.HasPrefix(m.Content, memoryPrefix) {
			t.Fatalf("mensagem de memória permaneceu no histórico: %+v", m)
		}
	}
}

func TestClearPreservaNotasPersistentes(t *testing.T) {
	a := newMemoryAgent()
	if err := a.Remember("projeto usa Go"); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	a.memory = append(a.memory, "- você: algo antigo")

	a.Clear()

	if len(a.memory) != 0 {
		t.Fatalf("Clear deveria zerar o resumo: %v", a.memory)
	}
	if !strings.Contains(a.history[1].Content, "projeto usa Go") {
		t.Fatalf("nota persistente deveria sobreviver ao Clear: %+v", a.history)
	}
}

func TestRememberTool(t *testing.T) {
	a := newMemoryAgent()
	tool := rememberTool{a: a}

	out, err := tool.Run(context.Background(), []byte(`{"note":"usa Go 1.22"}`))
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if !strings.Contains(out, "memória") {
		t.Fatalf("saída = %q", out)
	}
	if !strings.Contains(a.Memory(), "Go 1.22") {
		t.Fatalf("nota não gravada: %q", a.Memory())
	}
	if _, err := tool.Run(context.Background(), []byte(`{"note":""}`)); err == nil {
		t.Fatal("esperava erro para nota vazia")
	}
}

// fakeEmbedder devolve vetores determinísticos de 2 dimensões: textos que falam
// em "linguagem" caem no eixo 0, os demais no eixo 1.
type fakeEmbedder struct {
	calls int
	err   error
}

func (f *fakeEmbedder) Embed(_ context.Context, req llm.EmbeddingRequest) ([][]float32, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var texts []string
	switch in := req.Input.(type) {
	case string:
		texts = []string{in}
	case []string:
		texts = in
	}
	out := make([][]float32, len(texts))
	for i, s := range texts {
		if strings.Contains(strings.ToLower(s), "linguagem") {
			out[i] = []float32{1, 0}
		} else {
			out[i] = []float32{0, 1}
		}
	}
	return out, nil
}

func newRAGAgent(emb Embedder, topK int) *Agent {
	cfg := &config.Config{System: "s", MaxSteps: 4, RAG: true, TopK: topK}
	a := New(cfg, &fakeLLM{}, tools.NewRegistry(), safety.New(4, nil))
	a.SetEmbedder(emb)
	return a
}

// notesMessage devolve a mensagem de notas injetada no histórico (ou "").
func notesMessage(a *Agent) string {
	for _, m := range a.history {
		if strings.HasPrefix(m.Content, notesPrefix) {
			return m.Content
		}
	}
	return ""
}

func TestRetrieveNotesUsaSimilaridade(t *testing.T) {
	a := newRAGAgent(&fakeEmbedder{}, 1)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	a.retrieveNotes(context.Background(), "qual linguagem usamos?")

	got := notesMessage(a)
	if !strings.Contains(got, "linguagem Go") {
		t.Fatalf("nota relevante não foi injetada: %q", got)
	}
	if strings.Contains(got, "café") {
		t.Fatalf("nota irrelevante foi injetada com RAG: %q", got)
	}
	if !strings.Contains(a.Memory(), "[no contexto]") {
		t.Fatalf("Memory() deveria marcar a nota no contexto:\n%s", a.Memory())
	}
}

func TestRetrieveNotesFallbackSemEmbedder(t *testing.T) {
	a := newRAGAgent(nil, 1)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	a.retrieveNotes(context.Background(), "qual linguagem usamos?")

	got := notesMessage(a)
	if !strings.Contains(got, "linguagem Go") || !strings.Contains(got, "café") {
		t.Fatalf("sem embedder todas as notas deveriam ser injetadas: %q", got)
	}
}

func TestRetrieveNotesFallbackComErroDeEmbedding(t *testing.T) {
	a := newRAGAgent(&fakeEmbedder{err: errors.New("sem /embeddings")}, 1)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	a.retrieveNotes(context.Background(), "qual linguagem usamos?")

	got := notesMessage(a)
	if !strings.Contains(got, "linguagem Go") || !strings.Contains(got, "café") {
		t.Fatalf("com erro no /embeddings todas as notas deveriam ser injetadas: %q", got)
	}
}

func TestRetrieveNotesSemRAGInjetaTodas(t *testing.T) {
	cfg := &config.Config{System: "s", MaxSteps: 4, TopK: 1}
	a := New(cfg, &fakeLLM{}, tools.NewRegistry(), safety.New(4, nil))
	emb := &fakeEmbedder{}
	a.SetEmbedder(emb)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	a.retrieveNotes(context.Background(), "qual linguagem usamos?")

	if emb.calls != 0 {
		t.Fatalf("sem -rag não deveria chamar /embeddings (calls = %d)", emb.calls)
	}
	got := notesMessage(a)
	if !strings.Contains(got, "linguagem Go") || !strings.Contains(got, "café") {
		t.Fatalf("notas injetadas: %q", got)
	}
}

func TestEmbedOneCacheiaVetores(t *testing.T) {
	emb := &fakeEmbedder{}
	a := newRAGAgent(emb, 5)

	if _, err := a.embedOne(context.Background(), "mesmo texto"); err != nil {
		t.Fatalf("embedOne: %v", err)
	}
	if _, err := a.embedOne(context.Background(), "mesmo texto"); err != nil {
		t.Fatalf("embedOne: %v", err)
	}
	if emb.calls != 1 {
		t.Fatalf("embeddings = %d, esperava 1 (cache)", emb.calls)
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0}, []float32{1, 0}, 1},
		{[]float32{1, 0}, []float32{0, 1}, 0},
		{[]float32{1, 0}, []float32{1, 1}, 0.7071067811865475},
		{nil, []float32{1, 0}, 0},
		{[]float32{0, 0}, []float32{1, 0}, 0},
		{[]float32{1, 0}, []float32{1, 0, 0}, 0},
	}
	for _, tc := range cases {
		if got := cosineSimilarity(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("cosineSimilarity(%v, %v) = %v, esperava %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestMemoryLine(t *testing.T) {
	cases := []struct {
		msg  llm.Message
		want string
	}{
		{llm.Message{Role: llm.RoleUser, Content: "oi"}, "- você: oi"},
		{llm.Message{Role: llm.RoleAssistant, Content: "olá"}, "- assistente: olá"},
		{llm.Message{Role: llm.RoleUser, Content: "ERRO (x): falhou"}, "- harness: ERRO (x): falhou"},
		{llm.Message{Role: llm.RoleSystem, Content: "sistema"}, ""},
		{llm.Message{Role: llm.RoleUser, Content: "  \n "}, ""},
	}
	for _, tc := range cases {
		if got := memoryLine(tc.msg); got != tc.want {
			t.Fatalf("memoryLine(%+v) = %q, esperava %q", tc.msg, got, tc.want)
		}
	}
}
