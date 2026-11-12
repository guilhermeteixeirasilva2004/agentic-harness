package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPErrorRetryable(t *testing.T) {
	cases := map[int]bool{
		200: false, 400: false, 401: false, 403: false, 404: false, 413: false,
		408: true, 429: true, 500: true, 501: false, 502: true, 503: true, 504: true,
	}
	for status, want := range cases {
		if got := (&HTTPError{Status: status}).Retryable(); got != want {
			t.Fatalf("status %d: Retryable = %v; quero %v", status, got, want)
		}
	}
	msg := (&HTTPError{Status: 400, Body: strings.Repeat("x", 900), RetryAfter: time.Second}).Error()
	if !strings.Contains(msg, "400") || !strings.Contains(msg, "retry-after") {
		t.Fatalf("mensagem inesperada: %q", msg)
	}
	if len([]rune(msg)) > 600 {
		t.Fatalf("corpo do erro não foi truncado: %d caracteres", len([]rune(msg)))
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := ParseRetryAfter(""); got != 0 {
		t.Fatalf("vazio = %s", got)
	}
	if got := ParseRetryAfter("5"); got != 5*time.Second {
		t.Fatalf("segundos = %s", got)
	}
	if got := ParseRetryAfter("-2"); got != 0 {
		t.Fatalf("negativo = %s", got)
	}
	if got := ParseRetryAfter("lixo"); got != 0 {
		t.Fatalf("lixo = %s", got)
	}
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := ParseRetryAfter(future); got <= 0 || got > 31*time.Second {
		t.Fatalf("data futura = %s", got)
	}
	past := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	if got := ParseRetryAfter(past); got != 0 {
		t.Fatalf("data passada = %s", got)
	}
}

func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		name  string
		url   string
		allow bool
		ok    bool
	}{
		{"loopback ipv4", "http://127.0.0.1:8080/v1", false, true},
		{"localhost", "http://localhost:8080/v1", false, true},
		{"localhost sem esquema", "localhost:8080/v1", false, true},
		{"ipv6 loopback", "http://[::1]:8080/v1", false, true},
		{"https remoto", "https://api.exemplo.com/v1", false, true},
		{"http remoto", "http://api.exemplo.com/v1", false, false},
		{"http remoto com flag", "http://api.exemplo.com/v1", true, true},
		{"host:porta remoto", "192.168.0.10:8080/v1", false, false},
		{"vazio", "   ", false, false},
	}
	for _, tc := range cases {
		err := ValidateBaseURL(tc.url, tc.allow)
		if (err == nil) != tc.ok {
			t.Fatalf("%s: err = %v; quero ok=%v", tc.name, err, tc.ok)
		}
	}
}

func TestCompleteDevolveHTTPErrorTipado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"muitas requisições"}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).Complete(context.Background(), ChatRequest{Model: "m"})
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v; quero *HTTPError", err)
	}
	if httpErr.Status != http.StatusTooManyRequests || httpErr.RetryAfter != 7*time.Second {
		t.Fatalf("HTTPError = %+v", httpErr)
	}
	if !strings.Contains(httpErr.Body, "muitas requisições") {
		t.Fatalf("corpo perdido: %q", httpErr.Body)
	}
}

func TestCompleteDevolveErrProtocol(t *testing.T) {
	for _, body := range []string{`{`, `{"choices":[]}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		_, err := NewClient(srv.URL).Complete(context.Background(), ChatRequest{Model: "m"})
		srv.Close()
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("corpo %q: err = %v; quero ErrProtocol", body, err)
		}
	}
}

func TestEmbedDevolveErrProtocolParaIndiceInvalido(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1],"index":5}]}`))
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL).Embed(context.Background(), EmbeddingRequest{Input: "x"}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v; quero ErrProtocol", err)
	}
}

func TestChatDevolveErrTransportSemServidor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // porta fechada: conexão recusada

	if _, err := NewClient(url).Chat(context.Background(), ChatRequest{}, nil); !errors.Is(err, ErrTransport) {
		t.Fatalf("err = %v; quero ErrTransport", err)
	}
}

func TestChatDerrubaStreamTravado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Errorf("sem suporte a flush")
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		flusher.Flush()
		<-r.Context().Done() // fica mudo até o cliente desistir
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	c.streamIdle = 50 * time.Millisecond
	full, err := c.Chat(context.Background(), ChatRequest{}, nil)
	if !errors.Is(err, ErrTransport) {
		t.Fatalf("err = %v; quero ErrTransport", err)
	}
	if full != "a" {
		t.Fatalf("conteúdo parcial = %q; quero \"a\"", full)
	}
}
