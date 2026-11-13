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

// TestTrimHistoryResumeNaMemoria verifica pelo wire que as mensagens descartadas
// viram linhas do resumo (você/assistente/harness) e que um novo trim não
// duplica o bloco de memória.
func TestTrimHistoryResumeNaMemoria(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"final","answer":"` + strings.Repeat("a", 400) + `"}`,
		`{"action":"final","answer":"ok"}`,
		`{"action":"final","answer":"ok"}`,
		`{"action":"final","answer":"ok"}`,
	}}
	cfg := &config.Config{System: "s", MaxSteps: 4, MaxContextTokens: 10}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))

	entradas := []string{
		strings.Repeat("u", 400),
		"OBSERVAÇÃO (read_file): conteúdo",
		"pergunta atual",
	}
	for i, in := range entradas {
		if _, err := a.Turn(context.Background(), in, nil); err != nil {
			t.Fatalf("turno %d: %v", i+1, err)
		}
	}

	mem := f.requests[2].Messages[1].Content
	for _, want := range []string{"você: ", "assistente: ", "harness: OBSERVAÇÃO"} {
		if !strings.Contains(mem, want) {
			t.Fatalf("resumo sem %q: %q", want, mem)
		}
	}
	if !strings.Contains(a.Memory(), "resumo da conversa") {
		t.Fatalf("Memory() inesperado: %q", a.Memory())
	}

	// Um novo turno força outro trim: o resumo continua único no histórico.
	if _, err := a.Turn(context.Background(), strings.Repeat("z", 400), nil); err != nil {
		t.Fatalf("turno 4: %v", err)
	}
	count := 0
	for _, m := range f.requests[3].Messages {
		if strings.HasPrefix(m.Content, "MEMÓRIA DA CONVERSA (resumo acumulado):") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("resumo duplicado no histórico (%d ocorrências): %+v", count, f.requests[3].Messages)
	}
}

func TestRememberPersisteNoStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	f := &fakeLLM{}
	cfg := &config.Config{System: "teste", MaxSteps: 4}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))
	a.SetStore(st)
	a.SetSession("s1")
	if err := a.Remember("o usuário prefere respostas curtas"); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if _, err := a.Turn(context.Background(), "oi", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	if notas := notasNoContexto(f.requests[0].Messages); !strings.Contains(notas, "respostas curtas") {
		t.Fatalf("nota não foi injetada no contexto: %q", notas)
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
	f := &fakeLLM{}
	cfg := &config.Config{System: "teste", MaxSteps: 4}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))
	if err := a.Remember("fato qualquer"); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	a.Forget()

	if mem := a.Memory(); !strings.Contains(mem, "(nenhuma)") || !strings.Contains(mem, "(vazio)") {
		t.Fatalf("memória não foi limpa:\n%s", mem)
	}
	if _, err := a.Turn(context.Background(), "oi", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}
	for _, m := range f.requests[0].Messages {
		if strings.HasPrefix(m.Content, "FATOS LEMBRADOS") || strings.HasPrefix(m.Content, "MEMÓRIA DA CONVERSA") {
			t.Fatalf("mensagem de memória permaneceu no histórico: %+v", m)
		}
	}
}

func TestClearPreservaNotasPersistentes(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"final","answer":"ok"}`,
		`{"action":"final","answer":"ok"}`,
	}}
	cfg := &config.Config{System: "teste", MaxSteps: 4, MaxContextTokens: 10}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))
	if err := a.Remember("projeto usa Go"); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	// O excedente do segundo turno vira resumo (memória da conversa).
	if _, err := a.Turn(context.Background(), strings.Repeat("x", 400), nil); err != nil {
		t.Fatalf("turno 1: %v", err)
	}
	if _, err := a.Turn(context.Background(), "de novo", nil); err != nil {
		t.Fatalf("turno 2: %v", err)
	}
	if mem := a.Memory(); strings.Contains(mem, "(vazio)") {
		t.Fatalf("o resumo deveria existir antes do Clear:\n%s", mem)
	}

	a.Clear()

	mem := a.Memory()
	if !strings.Contains(mem, "(vazio)") {
		t.Fatalf("Clear deveria zerar o resumo:\n%s", mem)
	}
	if !strings.Contains(mem, "projeto usa Go") {
		t.Fatalf("nota persistente deveria sobreviver ao Clear:\n%s", mem)
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

func newRAGAgent(f *fakeLLM, emb Embedder, topK int) *Agent {
	cfg := &config.Config{System: "s", MaxSteps: 4, RAG: true, TopK: topK}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))
	a.SetEmbedder(emb)
	return a
}

// notasNoContexto devolve o bloco de notas injetado no contexto (ou "").
func notasNoContexto(msgs []llm.Message) string {
	for _, m := range msgs {
		if strings.HasPrefix(m.Content, "FATOS LEMBRADOS") {
			return m.Content
		}
	}
	return ""
}

func TestRetrieveNotesUsaSimilaridade(t *testing.T) {
	f := &fakeLLM{}
	a := newRAGAgent(f, &fakeEmbedder{}, 1)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	if _, err := a.Turn(context.Background(), "qual linguagem usamos?", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	got := notasNoContexto(f.requests[0].Messages)
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
	f := &fakeLLM{}
	a := newRAGAgent(f, nil, 1)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	if _, err := a.Turn(context.Background(), "qual linguagem usamos?", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	got := notasNoContexto(f.requests[0].Messages)
	if !strings.Contains(got, "linguagem Go") || !strings.Contains(got, "café") {
		t.Fatalf("sem embedder todas as notas deveriam ser injetadas: %q", got)
	}
}

func TestRetrieveNotesFallbackComErroDeEmbedding(t *testing.T) {
	f := &fakeLLM{}
	a := newRAGAgent(f, &fakeEmbedder{err: errors.New("sem /embeddings")}, 1)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	if _, err := a.Turn(context.Background(), "qual linguagem usamos?", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	got := notasNoContexto(f.requests[0].Messages)
	if !strings.Contains(got, "linguagem Go") || !strings.Contains(got, "café") {
		t.Fatalf("com erro no /embeddings todas as notas deveriam ser injetadas: %q", got)
	}
}

func TestRetrieveNotesSemRAGInjetaTodas(t *testing.T) {
	f := &fakeLLM{}
	cfg := &config.Config{System: "s", MaxSteps: 4, TopK: 1}
	a := New(cfg, f, tools.NewRegistry(), safety.New(4, nil))
	emb := &fakeEmbedder{}
	a.SetEmbedder(emb)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	if _, err := a.Turn(context.Background(), "qual linguagem usamos?", nil); err != nil {
		t.Fatalf("Turn: %v", err)
	}

	if emb.calls != 0 {
		t.Fatalf("sem -rag não deveria chamar /embeddings (calls = %d)", emb.calls)
	}
	got := notasNoContexto(f.requests[0].Messages)
	if !strings.Contains(got, "linguagem Go") || !strings.Contains(got, "café") {
		t.Fatalf("notas injetadas: %q", got)
	}
}

func TestEmbedOneCacheiaVetores(t *testing.T) {
	emb := &fakeEmbedder{}
	f := &fakeLLM{}
	a := newRAGAgent(f, emb, 1)
	for _, n := range []string{"o projeto usa a linguagem Go", "gosto de café sem açúcar"} {
		if err := a.Remember(n); err != nil {
			t.Fatalf("Remember: %v", err)
		}
	}

	// A mesma pergunta duas vezes: consulta e notas vêm do cache na segunda vez.
	for i := 0; i < 2; i++ {
		if _, err := a.Turn(context.Background(), "qual linguagem usamos?", nil); err != nil {
			t.Fatalf("turno %d: %v", i+1, err)
		}
	}
	if emb.calls != 3 {
		t.Fatalf("embeddings = %d, esperava 3 (consulta + 2 notas, com cache)", emb.calls)
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
