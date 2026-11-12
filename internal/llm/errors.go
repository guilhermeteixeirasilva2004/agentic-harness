package llm

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"agentic-harness/internal/textutil"
)

// ErrTransport indica falha de rede, DNS, conexão recusada ou timeout de
// transporte/stream. São os erros que valem retentativa.
var ErrTransport = errors.New("llm: falha de transporte")

// ErrProtocol indica que o servidor respondeu 2xx mas o corpo não é o esperado
// (JSON inválido, sem escolhas, índice inválido). Repetir não adianta.
var ErrProtocol = errors.New("llm: resposta inválida do servidor")

// HTTPError é uma resposta fora da faixa 2xx. A camada de retentativa usa
// Retryable e RetryAfter para decidir se repete e quanto esperar.
type HTTPError struct {
	Status int
	Body   string
	// RetryAfter é o atraso pedido pelo servidor em Retry-After (0 = ausente).
	RetryAfter time.Duration
}

// Error descreve o erro com o corpo truncado (o corpo pode ser enorme).
func (e *HTTPError) Error() string {
	msg := fmt.Sprintf("servidor respondeu %d", e.Status)
	if e.Body != "" {
		msg += ": " + textutil.TruncateRunes(e.Body, 500)
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry-after %s)", e.RetryAfter)
	}
	return msg
}

// Retryable informa se o status é transitório: 408, 429 e 5xx (menos 501) valem
// retentativa; 400/401/403/404/413 etc. são erros do pedido e não devem repetir.
func (e *HTTPError) Retryable() bool {
	if e.Status == http.StatusRequestTimeout || e.Status == http.StatusTooManyRequests {
		return true
	}
	return e.Status >= 500 && e.Status != http.StatusNotImplemented
}

// ParseRetryAfter converte o cabeçalho Retry-After (segundos ou data HTTP) em
// duração. Valores inválidos ou no passado resultam em 0.
func ParseRetryAfter(v string) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// ValidateBaseURL recusa http:// para host que não seja loopback, a menos que
// allowInsecure seja true: nesse caso o prompt, as respostas e o conteúdo dos
// arquivos trafegariam em claro pela rede.
func ValidateBaseURL(raw string, allowInsecure bool) error {
	if allowInsecure {
		return nil
	}
	u, err := parseBaseURL(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "http" {
		return nil
	}
	if isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("URL %q usa http:// para host remoto (%s): use https:// ou -allow-insecure-http", raw, u.Host)
}

// parseBaseURL aceita tanto "https://host/v1" quanto "host:porta/v1" (sem
// esquema, tratado como http).
func parseBaseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("URL do servidor vazia")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("URL inválida %q: %w", raw, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("URL sem host: %q", raw)
	}
	return u, nil
}

// isLoopbackHost reconhece localhost, *.localhost e IPs de loopback.
func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
