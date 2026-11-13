package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentic-harness/internal/config"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/memory"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/store"
	"agentic-harness/internal/tools"
)

// fakeServer simula um servidor compatível com OpenAI (llama-server) no ar:
// devolve decisões roteirizadas e registra as requisições recebidas. É o E2E em
// Go — sem depender de Python ou de um modelo real.
type fakeServer struct {
	mu         sync.Mutex
	responses  []string
	statuses   []int // status HTTP por chamada (vazio ou 0 = 200)
	requests   []llm.ChatRequest
	served     int // quantas respostas roteirizadas já foram entregues
	embedCalls int
	// streamText é o texto devolvido quando a requisição pede streaming (a
	// resposta final do agente com -stream).
	streamText string
}

func (s *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/chat/completions":
		s.serveChat(w, r)
	case "/v1/embeddings":
		s.serveEmbeddings(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *fakeServer) serveChat(w http.ResponseWriter, r *http.Request) {
	var req llm.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.requests = append(s.requests, req)
	if req.Stream {
		// Resposta final em streaming: não consome o roteiro de decisões.
		text := s.streamText
		if text == "" {
			text = "resposta em stream"
		}
		s.mu.Unlock()
		writeSSE(w, text)
		return
	}
	idx := len(s.requests) - 1
	status := http.StatusOK
	if idx < len(s.statuses) && s.statuses[idx] != 0 {
		status = s.statuses[idx]
	}
	// Só respostas bem-sucedidas consomem um roteiro, de modo que as falhas
	// injetadas não desalinham a lista de respostas esperadas.
	content := `{"action":"final","answer":"fim"}`
	if status == http.StatusOK {
		if s.served < len(s.responses) {
			content = s.responses[s.served]
		}
		s.served++
	}
	s.mu.Unlock()

	if status != http.StatusOK {
		http.Error(w, "falha transitória", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"role": "assistant", "content": content}},
		},
	})
}

func (s *fakeServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// serveEmbeddings devolve vetores determinísticos: textos que falam em
// "linguagem" caem no eixo 0, os demais no eixo 1.
func (s *fakeServer) serveEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req llm.EmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var texts []string
	switch in := req.Input.(type) {
	case string:
		texts = []string{in}
	case []any:
		for _, item := range in {
			if str, ok := item.(string); ok {
				texts = append(texts, str)
			}
		}
	}
	s.mu.Lock()
	s.embedCalls++
	s.mu.Unlock()

	data := make([]map[string]any, len(texts))
	for i, text := range texts {
		vec := []float32{0, 1}
		if strings.Contains(strings.ToLower(text), "linguagem") {
			vec = []float32{1, 0}
		}
		data[i] = map[string]any{"index": i, "embedding": vec}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (s *fakeServer) lastRequest() llm.ChatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return llm.ChatRequest{}
	}
	return s.requests[len(s.requests)-1]
}

func (s *fakeServer) requestAt(i int) llm.ChatRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.requests) {
		return llm.ChatRequest{}
	}
	return s.requests[i]
}

// newE2EAgent monta o agente falando HTTP com o servidor falso, com as tools
// remember/ask_user registradas como em NewDefault (sem as tools de arquivo).
func newE2EAgent(t *testing.T, srv *fakeServer, cfg *config.Config) *Agent {
	t.Helper()
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	cfg.BaseURL = ts.URL + "/v1"
	if cfg.Model == "" {
		cfg.Model = "teste"
	}
	if cfg.System == "" {
		cfg.System = "teste"
	}
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 4
	}
	allow := cfg.AllowTools
	if len(allow) == 0 {
		allow = []string{"remember", "ask_user"}
	}
	reg := tools.NewRegistry()
	reg.Allow(allow...)
	client := llm.NewClient(cfg.BaseURL)
	a := New(cfg, client, reg, safety.New(cfg.MaxSteps, allow))
	a.SetEmbedder(client)
	reg.Add(rememberTool{a: a})
	reg.Add(askUserTool{a: a})
	return a
}

// writeSSE emite o texto em pedaços de 3 bytes no formato SSE de chat, para
// exercitar o caminho de streaming do cliente.
func writeSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	for len(text) > 0 {
		n := 3
		if n > len(text) {
			n = len(text)
		}
		payload, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"delta": map[string]any{"content": text[:n]}}},
		})
		fmt.Fprintf(w, "data: %s\n\n", payload)
		if fl != nil {
			fl.Flush()
		}
		text = text[n:]
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

func TestE2EStreamingDaRespostaFinal(t *testing.T) {
	srv := &fakeServer{
		responses:  []string{`{"action":"final","answer":"curta"}`},
		streamText: "resposta final completa",
	}
	a := newE2EAgent(t, srv, &config.Config{Stream: true})

	var deltas []string
	out, err := a.Turn(context.Background(), "oi", func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "resposta final completa" {
		t.Fatalf("out = %q", out)
	}
	if got := strings.Join(deltas, ""); got != "resposta final completa" {
		t.Fatalf("deltas = %q", got)
	}
	if len(deltas) < 2 {
		t.Fatalf("esperava streaming em vários pedaços, veio %d", len(deltas))
	}
	if first := srv.requestAt(0); first.Stream || first.ResponseFormat == nil {
		t.Fatalf("a decisão deveria ser sem stream e com json_schema: stream=%v rf=%+v", first.Stream, first.ResponseFormat)
	}
	if last := srv.lastRequest(); !last.Stream {
		t.Fatal("a resposta final deveria ter sido pedida em streaming")
	}
}

func TestE2EAgenteUsaToolEGravaMemoria(t *testing.T) {
	srv := &fakeServer{responses: []string{
		`{"action":"tool","tool":"remember","arguments":{"note":"o time usa Go 1.22"}}`,
		`{"action":"final","answer":"anotado"}`,
	}}
	a := newE2EAgent(t, srv, &config.Config{})

	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	a.SetStore(st)
	a.SetSession("e2e")

	out, err := a.Turn(context.Background(), "lembre que usamos Go", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "anotado" {
		t.Fatalf("out = %q", out)
	}

	// A decisão foi pedida sem streaming e com json_schema.
	first := srv.requestAt(0)
	if first.Stream {
		t.Fatal("o loop do agente deveria chamar sem streaming")
	}
	if first.ResponseFormat == nil || first.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format = %+v", first.ResponseFormat)
	}

	// A observação da tool voltou para o modelo na segunda chamada.
	var observed bool
	for _, m := range srv.lastRequest().Messages {
		if strings.Contains(m.Content, "OBSERVAÇÃO (remember)") {
			observed = true
		}
	}
	if !observed {
		t.Fatalf("observação da tool não foi realimentada: %+v", srv.lastRequest().Messages)
	}

	// A nota aparece na memória e foi persistida no NDJSON.
	if !strings.Contains(a.Memory(), "Go 1.22") {
		t.Fatalf("Memory() = %q", a.Memory())
	}
	recs, err := st.Query()
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var persisted bool
	for _, r := range recs {
		if r.Type == "memory" && strings.Contains(string(r.Payload), "Go 1.22") {
			persisted = true
		}
	}
	if !persisted {
		t.Fatalf("registro de memória não persistido: %+v", recs)
	}
	if a.SessionID() != "e2e" {
		t.Fatalf("sessão = %q", a.SessionID())
	}
}

func TestNewDefaultLigaEmbedderEToolsDeInteracao(t *testing.T) {
	cfg := &config.Config{MaxSteps: 2, AllowTools: []string{"get_time", "remember", "ask_user"}}
	a := NewDefault(cfg)

	if a.embed == nil {
		t.Fatal("NewDefault deveria ligar o embedder (RAG leve)")
	}
	for _, name := range []string{"remember", "ask_user"} {
		if _, ok := a.reg.Get(name); !ok {
			t.Fatalf("%s deveria estar registrada", name)
		}
	}
	// Sem canal interativo, ask_user falha de forma explícita (e não travava).
	if _, err := a.reg.Dispatch(context.Background(), tools.Call{
		Tool:      "ask_user",
		Arguments: []byte(`{"question":"qual o nome?"}`),
	}); err == nil {
		t.Fatal("esperava erro sem canal de interação")
	}
}

func TestE2ERAGSelecionaNotasRelevantes(t *testing.T) {
	srv := &fakeServer{responses: []string{`{"action":"final","answer":"ok"}`}}
	cfg := &config.Config{RAG: true, TopK: 1, EmbedModel: "emb"}
	a := newE2EAgent(t, srv, cfg)
	if err := a.Remember("o projeto usa a linguagem Go"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	if err := a.Remember("gosto de café sem açúcar"); err != nil {
		t.Fatalf("Remember: %v", err)
	}

	if _, err := a.Turn(context.Background(), "qual linguagem usamos?", nil); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if srv.embedCalls == 0 {
		t.Fatal("com -rag o agente deveria chamar /embeddings")
	}

	var notes string
	for _, m := range srv.lastRequest().Messages {
		if strings.HasPrefix(m.Content, memory.NotesPrefix) {
			notes = m.Content
		}
	}
	if !strings.Contains(notes, "linguagem Go") {
		t.Fatalf("nota relevante não foi injetada: %q", notes)
	}
	if strings.Contains(notes, "café") {
		t.Fatalf("nota irrelevante foi injetada: %q", notes)
	}
}

func TestE2ERetentativaSobreHTTP(t *testing.T) {
	srv := &fakeServer{
		responses: []string{`{"action":"final","answer":"ok"}`},
		statuses:  []int{http.StatusServiceUnavailable, http.StatusOK},
	}
	a := newE2EAgent(t, srv, &config.Config{Retries: 1, Backoff: time.Millisecond})

	out, err := a.Turn(context.Background(), "oi", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q", out)
	}
	if n := srv.requestCount(); n != 2 {
		t.Fatalf("requisições = %d, esperava 2 (1 falha + 1 retentativa)", n)
	}
	if s := a.meter.Snapshot(); s.Retries != 1 {
		t.Fatalf("Retries = %d, esperava 1", s.Retries)
	}
}

func TestE2EAskUserNoLoop(t *testing.T) {
	srv := &fakeServer{responses: []string{
		`{"action":"tool","tool":"ask_user","arguments":{"question":"qual o projeto?"}}`,
		`{"action":"final","answer":"valeu"}`,
	}}
	a := newE2EAgent(t, srv, &config.Config{})
	as := &fakeAsker{answer: "agentic-harness"}
	a.SetAsker(as)

	out, err := a.Turn(context.Background(), "me ajude", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "valeu" {
		t.Fatalf("out = %q", out)
	}
	if len(as.asked) != 1 {
		t.Fatalf("perguntas = %#v", as.asked)
	}
	var answered bool
	for _, m := range srv.lastRequest().Messages {
		if strings.Contains(m.Content, "agentic-harness") {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("resposta do usuário não chegou ao modelo: %+v", srv.lastRequest().Messages)
	}
}
