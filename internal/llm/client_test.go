package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamDeltas(t *testing.T) {
	const stream = "" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Olá\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" mundo\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{}}]}\n\n" +
		"\n" +
		"data: [DONE]\n\n"

	var got strings.Builder
	full, err := streamDeltas(strings.NewReader(stream), func(d string) { got.WriteString(d) })
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if want := "Olá mundo"; full != want {
		t.Fatalf("texto completo = %q, esperava %q", full, want)
	}
	if want := "Olá mundo"; got.String() != want {
		t.Fatalf("deltas = %q, esperava %q", got.String(), want)
	}
}

func TestStreamDeltasIgnoraLinhasInvalidas(t *testing.T) {
	const stream = ": comentário\n" +
		"data: not-json\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"data: [DONE]\n\n"

	full, err := streamDeltas(strings.NewReader(stream), nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if full != "ok" {
		t.Fatalf("texto completo = %q, esperava %q", full, "ok")
	}
}

func TestComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("caminho = %q, esperava /v1/chat/completions", r.URL.Path)
		}
		var body ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("corpo inválido: %v", err)
		}
		if body.Stream {
			t.Errorf("stream deveria ser false no modo Complete")
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL + "/v1")
	got, err := c.Complete(context.Background(), ChatRequest{Model: "m"})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if got != "ok" {
		t.Fatalf("resposta = %q, esperava %q", got, "ok")
	}
}

func TestEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("caminho = %q, esperava /v1/embeddings", r.URL.Path)
		}
		// fora de ordem de propósito: valida o uso do campo index
		_, _ = w.Write([]byte(`{"data":[{"embedding":[3,4],"index":1},{"embedding":[1,2],"index":0}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL + "/v1")
	vecs, err := c.Embed(context.Background(), EmbeddingRequest{Input: []string{"a", "b"}})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 2 || vecs[0][0] != 1 || vecs[1][0] != 3 {
		t.Fatalf("embeddings inesperados: %v", vecs)
	}
}
