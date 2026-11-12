// Package llm implementa um cliente mínimo para a API de chat compatível com OpenAI.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// streamIdleTimeout é o tempo máximo sem nenhum byte no stream antes de
// considerar a conexão travada. Sem isso, um servidor que pendura a resposta sem
// fechar o socket prende o agente para sempre.
const streamIdleTimeout = 60 * time.Second

// Client fala com um servidor compatível com OpenAI (ex.: llama-server).
type Client struct {
	baseURL string
	http    *http.Client
	// streamIdle é o tempo máximo sem dados no streaming (0 = streamIdleTimeout).
	streamIdle time.Duration
}

// NewClient cria um cliente para a base URL informada (ex.: http://127.0.0.1:8080/v1).
// O transporte tem timeouts de dial e de cabeçalho; o timeout da resposta em si é
// controlado pelo contexto da chamada, porque a geração pode ser longa.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Transport: defaultTransport()},
	}
}

// NewClientWithHTTP permite injetar um *http.Client próprio (testes, proxies).
func NewClientWithHTTP(baseURL string, hc *http.Client) *Client {
	c := NewClient(baseURL)
	if hc != nil {
		c.http = hc
	}
	return c
}

// defaultTransport monta o transporte com timeouts. Não há timeout global no
// http.Client: uma resposta em streaming pode durar minutos legitimamente.
func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// O primeiro byte já vem no cabeçalho da resposta SSE; 2 min cobrem
		// modelos lentos em CPU sem prender o agente se o servidor ficar mudo.
		ResponseHeaderTimeout: 120 * time.Second,
	}
}

// post envia payload em JSON para path e devolve a resposta 2xx (o chamador
// fecha o corpo). Erros de transporte viram ErrTransport; status fora de 2xx
// vira *HTTPError, para que a camada de retentativa decida o que repetir.
func (c *Client) post(ctx context.Context, path, accept string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("codificar requisição: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("montar requisição: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if accept != "" {
		httpReq.Header.Set("Accept", accept)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		// Preserva o erro original (*url.Error embrulha cancelamento/timeout),
		// mas marca a origem como transporte.
		return nil, fmt.Errorf("%w: %w", ErrTransport, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &HTTPError{
			Status:     resp.StatusCode,
			Body:       strings.TrimSpace(string(b)),
			RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After")),
		}
	}
	return resp, nil
}

// Chat envia a conversa, faz streaming token a token via onDelta e retorna o
// texto completo.
func (c *Client) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (string, error) {
	req.Stream = true
	resp, err := c.post(ctx, "/chat/completions", "text/event-stream", req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	idle := c.streamIdle
	if idle <= 0 {
		idle = streamIdleTimeout
	}
	watchdog := newIdleWatchdog(resp.Body, idle)
	defer watchdog.Stop()

	full, err := streamDeltas(watchdog, onDelta)
	if watchdog.TimedOut() {
		return full, fmt.Errorf("%w: stream ficou %s sem novos dados", ErrTransport, idle)
	}
	if err != nil {
		return full, fmt.Errorf("%w: %w", ErrTransport, err)
	}
	return full, nil
}

// streamDeltas lê o fluxo SSE e acumula o conteúdo, chamando onDelta a cada pedaço.
func streamDeltas(r io.Reader, onDelta func(string)) (string, error) {
	var full strings.Builder
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue // ignora linhas que não são JSON válido
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			full.WriteString(choice.Delta.Content)
			if onDelta != nil {
				onDelta(choice.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return full.String(), fmt.Errorf("ler stream: %w", err)
	}
	return full.String(), nil
}

// Complete envia a conversa sem streaming e devolve o conteúdo da primeira
// escolha. É usado pelo loop do agente, onde a resposta é um objeto JSON.
func (c *Client) Complete(ctx context.Context, req ChatRequest) (string, error) {
	req.Stream = false
	resp, err := c.post(ctx, "/chat/completions", "application/json", req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("%w: decodificar resposta: %w", ErrProtocol, err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%w: resposta sem escolhas", ErrProtocol)
	}
	return out.Choices[0].Message.Content, nil
}

// Embed gera embeddings para um texto ou lista de textos em /embeddings.
func (c *Client) Embed(ctx context.Context, req EmbeddingRequest) ([][]float32, error) {
	resp, err := c.post(ctx, "/embeddings", "", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var out EmbeddingResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decodificar resposta: %w", ErrProtocol, err)
	}
	vecs := make([][]float32, len(out.Data))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("%w: índice de embedding inválido: %d", ErrProtocol, d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}

// idleWatchdog fecha o corpo se o stream ficar idle por tempo demais: fechar o
// socket desbloqueia o Read em andamento e o erro chega como ErrTransport.
type idleWatchdog struct {
	body     io.ReadCloser
	idle     time.Duration
	timer    *time.Timer
	timedOut atomic.Bool
}

func newIdleWatchdog(body io.ReadCloser, idle time.Duration) *idleWatchdog {
	w := &idleWatchdog{body: body, idle: idle}
	w.timer = time.AfterFunc(idle, func() {
		w.timedOut.Store(true)
		_ = body.Close()
	})
	return w
}

func (w *idleWatchdog) Read(p []byte) (int, error) {
	n, err := w.body.Read(p)
	if n > 0 {
		w.timer.Reset(w.idle)
	}
	return n, err
}

// Stop encerra o vigia (chamado ao fim do stream).
func (w *idleWatchdog) Stop() { w.timer.Stop() }

// TimedOut informa se o vigia derrubou a conexão por inatividade.
func (w *idleWatchdog) TimedOut() bool { return w.timedOut.Load() }
