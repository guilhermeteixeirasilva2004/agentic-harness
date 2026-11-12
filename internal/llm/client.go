// Package llm implementa um cliente mínimo para a API de chat compatível com OpenAI.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Client fala com um servidor compatível com OpenAI (ex.: llama-server).
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient cria um cliente para a base URL informada (ex.: http://127.0.0.1:8080/v1).
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{},
	}
}

// Chat envia a conversa, faz streaming token a token via onDelta e retorna o texto completo.
func (c *Client) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (string, error) {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("codificar requisição: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("montar requisição: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("conectar ao servidor: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("servidor respondeu %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	return streamDeltas(resp.Body, onDelta)
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
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("codificar requisição: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("montar requisição: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("conectar ao servidor: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("servidor respondeu %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out ChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("decodificar resposta: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("resposta sem escolhas")
	}
	return out.Choices[0].Message.Content, nil
}

// Embed gera embeddings para um texto ou lista de textos em /embeddings.
func (c *Client) Embed(ctx context.Context, req EmbeddingRequest) ([][]float32, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("codificar requisição: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("montar requisição: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("conectar ao servidor: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("servidor respondeu %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}

	var out EmbeddingResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decodificar resposta: %w", err)
	}
	vecs := make([][]float32, len(out.Data))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("índice de embedding inválido: %d", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return vecs, nil
}
