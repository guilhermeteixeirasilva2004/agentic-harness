package agent

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"agentic-harness/internal/llm"
)

// defaultBackoff é a espera inicial entre retentativas quando a config não
// define outra.
const defaultBackoff = 500 * time.Millisecond

// maxRetryAfter limita a espera pedida pelo servidor em Retry-After: um valor
// absurdo (horas) travaria o turno.
const maxRetryAfter = 30 * time.Second

// complete chama o modelo com retentativas e backoff exponencial. Só falhas
// transitórias são repetidas (ver retryable); cancelamento ou expiração do
// contexto encerra na hora.
//
// A métrica de latência é registrada por tentativa, para que /metrics reflita o
// custo real das retentativas.
func (a *Agent) complete(ctx context.Context, req llm.ChatRequest) (string, error) {
	attempts := a.cfg.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	wait := a.cfg.Backoff
	if wait <= 0 {
		wait = defaultBackoff
	}

	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			a.meter.RecordRetry()
			delay := wait
			if d := retryAfter(lastErr); d > 0 {
				// O servidor mandou esperar: respeita, em vez do backoff local.
				delay = d
			} else {
				delay = jittered(wait)
			}
			a.logf("retentativa %d/%d em %s (último erro: %v)", i+1, attempts, delay, lastErr)
			if err := sleepCtx(ctx, delay); err != nil {
				return "", err
			}
			wait *= 2
		}
		started := time.Now()
		out, err := a.llm.Complete(ctx, req)
		a.meter.RecordLLM(time.Since(started))
		if err == nil {
			return out, nil
		}
		lastErr = err
		if !retryable(ctx, err) {
			return "", err
		}
	}
	return "", fmt.Errorf("falha após %d tentativa(s): %w", attempts, lastErr)
}

// retryable indica se vale repetir a chamada: apenas falhas de transporte
// (rede, DNS, timeout, stream travado) e status transitórios (408, 429 e 5xx,
// menos 501). Erros de protocolo (JSON inválido, resposta sem escolhas) e 4xx de
// pedido inválido não são repetidos. Cancelamentos do próprio contexto nunca.
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var httpErr *llm.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Retryable()
	}
	return errors.Is(err, llm.ErrTransport)
}

// retryAfter devolve o Retry-After pedido pelo servidor, limitado a
// maxRetryAfter (0 = o servidor não pediu espera).
func retryAfter(err error) time.Duration {
	var httpErr *llm.HTTPError
	if !errors.As(err, &httpErr) || httpErr.RetryAfter <= 0 {
		return 0
	}
	if httpErr.RetryAfter > maxRetryAfter {
		return maxRetryAfter
	}
	return httpErr.RetryAfter
}

// jittered aplica ±20% de ruído ao backoff, para que vários clientes não
// retentem todos no mesmo instante. Durações minúsculas (testes) ficam intactas.
func jittered(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	spread := d / 5
	if spread <= 0 {
		return d
	}
	delta := time.Duration(rand.Int63n(int64(2*spread)+1)) - spread
	return d + delta
}

// sleepCtx espera d ou até o contexto ser cancelado.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
