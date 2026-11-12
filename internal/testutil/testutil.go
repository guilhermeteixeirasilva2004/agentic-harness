// Package testutil fornece dublês de teste (fakes) compartilhados pelos pacotes
// internos. Depende apenas de pacotes-folha (llm e store), para que os testes
// internos possam usá-lo sem criar ciclos de importação.
package testutil

import (
	"context"
	"errors"
	"sync"
	"time"

	"agentic-harness/internal/llm"
)

// ErrNoReply é devolvido quando o FakeLLM é chamado sem respostas roteirizadas.
var ErrNoReply = errors.New("testutil: sem respostas roteirizadas")

// Reply é uma resposta roteirizada devolvida pelo FakeLLM. Quando Err não é
// nulo, a chamada falha com esse erro; Chunks alimenta o streaming pedaço a
// pedaço e Delay simula latência respeitando o contexto.
type Reply struct {
	Content string
	Err     error
	Chunks  []string
	Delay   time.Duration
}

// Text cria uma resposta de texto simples.
func Text(content string) Reply { return Reply{Content: content} }

// Fail cria uma resposta que falha com err.
func Fail(err error) Reply { return Reply{Err: err} }

// FakeLLM é um cliente de modelo de mentira: devolve respostas roteirizadas na
// ordem em que foram enfileiradas e grava as requisições recebidas. É seguro
// para uso concorrente.
type FakeLLM struct {
	mu       sync.Mutex
	replies  []Reply
	requests []llm.ChatRequest
	embeds   []llm.EmbeddingRequest
	embedFn  func(llm.EmbeddingRequest) ([][]float32, error)
}

// NewFakeLLM cria um FakeLLM já com as respostas informadas na fila.
func NewFakeLLM(replies ...Reply) *FakeLLM { return &FakeLLM{replies: replies} }

// Push enfileira mais respostas.
func (f *FakeLLM) Push(replies ...Reply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, replies...)
}

// Pending devolve quantas respostas ainda estão na fila.
func (f *FakeLLM) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.replies)
}

// Requests devolve uma cópia das requisições recebidas até agora.
func (f *FakeLLM) Requests() []llm.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]llm.ChatRequest, len(f.requests))
	for i, r := range f.requests {
		out[i] = cloneRequest(r)
	}
	return out
}

// LastRequest devolve a última requisição recebida.
func (f *FakeLLM) LastRequest() (llm.ChatRequest, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return llm.ChatRequest{}, false
	}
	return cloneRequest(f.requests[len(f.requests)-1]), true
}

// Complete implementa o contrato usado pelo loop do agente.
func (f *FakeLLM) Complete(ctx context.Context, req llm.ChatRequest) (string, error) {
	r, err := f.next(ctx, req, nil)
	if err != nil {
		return "", err
	}
	return r.Content, nil
}

// Chat implementa o contrato de streaming (llm.Streamer): os pedaços de Chunks
// (ou o Content inteiro) são repassados a onDelta antes de a resposta voltar.
func (f *FakeLLM) Chat(ctx context.Context, req llm.ChatRequest, onDelta func(string)) (string, error) {
	r, err := f.next(ctx, req, onDelta)
	if err != nil {
		return "", err
	}
	return r.Content, nil
}

// Embed implementa o contrato de embeddings; a resposta vem de SetEmbedFunc.
func (f *FakeLLM) Embed(ctx context.Context, req llm.EmbeddingRequest) ([][]float32, error) {
	f.mu.Lock()
	f.embeds = append(f.embeds, req)
	fn := f.embedFn
	f.mu.Unlock()
	if fn == nil {
		return nil, ErrNoReply
	}
	return fn(req)
}

// EmbedRequests devolve os pedidos de embedding recebidos.
func (f *FakeLLM) EmbedRequests() []llm.EmbeddingRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]llm.EmbeddingRequest(nil), f.embeds...)
}

// SetEmbedFunc define como o FakeLLM responde aos pedidos de embedding.
func (f *FakeLLM) SetEmbedFunc(fn func(llm.EmbeddingRequest) ([][]float32, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.embedFn = fn
}

// next desenfileira a próxima resposta, registra a requisição e aplica o atraso.
func (f *FakeLLM) next(ctx context.Context, req llm.ChatRequest, emit func(string)) (Reply, error) {
	f.mu.Lock()
	f.requests = append(f.requests, cloneRequest(req))
	if len(f.replies) == 0 {
		f.mu.Unlock()
		return Reply{}, ErrNoReply
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	f.mu.Unlock()

	if r.Delay > 0 {
		timer := time.NewTimer(r.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return Reply{}, ctx.Err()
		case <-timer.C:
		}
	}
	if r.Err != nil {
		return r, r.Err
	}
	if emit != nil {
		chunks := r.Chunks
		if len(chunks) == 0 {
			chunks = []string{r.Content}
		}
		for _, c := range chunks {
			emit(c)
		}
	}
	return r, nil
}

// cloneRequest copia a requisição, inclusive o slice de mensagens: o histórico
// do agente é reutilizado entre chamadas (B-11) e o fake não deve observá-lo
// mudar depois do fato.
func cloneRequest(req llm.ChatRequest) llm.ChatRequest {
	out := req
	out.Messages = append([]llm.Message(nil), req.Messages...)
	return out
}
