package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// defaultJobTimeout é o limite de execução de um comando em segundo plano quando
// a config não define outro.
const defaultJobTimeout = 60 * time.Second

// Job é um comando de shell executado em segundo plano (run_shell_bg).
type Job struct {
	ID      string
	Command string
	Dir     string

	done     chan struct{}
	buf      *limitedBuffer
	err      error
	started  time.Time
	finished time.Time
}

// Jobs mantém os comandos em segundo plano de uma sessão do agente. É seguro
// para uso concorrente.
type Jobs struct {
	mu  sync.Mutex
	m   map[string]*Job
	seq int
	max int // limite de jobs retidos
}

// NewJobs cria um registro de jobs em segundo plano.
func NewJobs() *Jobs {
	return &Jobs{m: make(map[string]*Job), max: 32}
}

// Start dispara o comando em segundo plano e devolve o job criado. A saída é
// acumulada até limit bytes; o comando roda com cwd em dir e timeout próprio.
// O contexto do turno não é herdado de propósito: o job sobrevive às chamadas
// de wait e só morre com o processo.
func (j *Jobs) Start(cmd, dir string, timeout time.Duration, limit int) *Job {
	if timeout <= 0 {
		timeout = defaultJobTimeout
	}
	if limit <= 0 {
		limit = 64 * 1024
	}
	job := &Job{
		Command: cmd,
		Dir:     dir,
		done:    make(chan struct{}),
		buf:     &limitedBuffer{limit: limit},
		started: time.Now(),
	}

	j.mu.Lock()
	j.seq++
	job.ID = fmt.Sprintf("job-%d", j.seq)
	j.m[job.ID] = job
	j.evictLocked()
	j.mu.Unlock()

	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		c := exec.CommandContext(runCtx, "sh", "-c", cmd)
		c.Dir = dir
		c.Stdout = job.buf
		c.Stderr = job.buf
		runErr := c.Run()
		job.finished = time.Now()
		switch {
		case runCtx.Err() == context.DeadlineExceeded:
			runErr = fmt.Errorf("comando excedeu o timeout de %s", timeout)
		case runErr != nil:
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				runErr = fmt.Errorf("comando saiu com código %d: %s", exitErr.ExitCode(), firstLine(job.buf.String()))
			}
		}
		job.err = runErr
		close(job.done)
	}()
	return job
}

// Wait aguarda o job terminar (ou o contexto/timeout expirar) e devolve a saída
// acumulada. Com timeout zero, espera até o fim do job ou do contexto.
func (j *Jobs) Wait(ctx context.Context, id string, timeout time.Duration) (string, error) {
	job, ok := j.get(id)
	if !ok {
		return "", fmt.Errorf("job desconhecido: %q", id)
	}
	waitCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	select {
	case <-job.done:
		out := job.buf.String()
		if job.err != nil {
			return out, job.err
		}
		if strings.TrimSpace(out) == "" {
			return "(sem saída)", nil
		}
		return out, nil
	case <-waitCtx.Done():
		return job.buf.String(), fmt.Errorf("job %s ainda em execução (use wait novamente)", job.ID)
	}
}

func (j *Jobs) get(id string) (*Job, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.m[id]
	return job, ok
}

// evictLocked descarta jobs já concluídos quando o registro passa do limite.
func (j *Jobs) evictLocked() {
	if len(j.m) <= j.max {
		return
	}
	for id, job := range j.m {
		if len(j.m) <= j.max {
			return
		}
		select {
		case <-job.done:
			delete(j.m, id)
		default:
		}
	}
}
