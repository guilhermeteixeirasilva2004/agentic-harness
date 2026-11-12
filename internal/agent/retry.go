package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agentic-harness/internal/llm"
)

// defaultBackoff é a espera inicial entre retentativas quando a config não
// define outra.
const defaultBackoff = 500 * time.Millisecond

// complete chama o modelo com retentativas e backoff exponencial. Falhas
// transitórias (rede, timeout do servidor, 5xx) são repetidas até cfg.Retries
// vezes; cancelamento ou expiração do contexto encerra na hora.
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
			a.logf("retentativa %d/%d em %s (último erro: %v)", i+1, attempts, wait, lastErr)
			if err := sleepCtx(ctx, wait); err != nil {
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

// retryable indica se vale repetir a chamada. Cancelamentos e expirações do
// próprio contexto nunca são repetidos.
func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
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
