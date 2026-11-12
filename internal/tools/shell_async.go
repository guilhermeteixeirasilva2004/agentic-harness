package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RunShellBG inicia um comando de shell em segundo plano (mesmas restrições de
// allowlist/metacaracteres de RunShell) e devolve um job_id para usar com a tool
// wait. Exige aprovação, como o run_shell síncrono.
type RunShellBG struct {
	Root     string   // raiz permitida (vazio = diretório atual)
	Allow    []string // prefixos de comando liberados
	Timeout  time.Duration
	MaxBytes int
	Jobs     *Jobs
}

func (RunShellBG) Name() string { return "run_shell_bg" }
func (RunShellBG) Description() string {
	return "Inicia um comando de shell permitido em segundo plano e devolve um job_id; use wait para coletar a saída."
}

func (RunShellBG) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "comando simples, ex.: \"go test ./...\""},
			"cwd":     map[string]any{"type": "string", "description": "diretório de trabalho relativo à raiz (vazio = raiz)"},
		},
		"required": []string{"command"},
	}
}

func (RunShellBG) RequiresApproval() bool { return true }

func (t RunShellBG) Run(_ context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Command string `json:"command"`
		Cwd     string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	cmdStr := strings.TrimSpace(in.Command)
	if cmdStr == "" {
		return "", errors.New(`argumento "command" é obrigatório`)
	}
	if err := CheckCommand(cmdStr, t.Allow); err != nil {
		return "", err
	}
	dir, err := resolveWithin(t.Root, in.Cwd)
	if err != nil {
		return "", err
	}
	if t.Jobs == nil {
		return "", errors.New("registro de jobs indisponível")
	}
	job := t.Jobs.Start(cmdStr, dir, t.Timeout, t.MaxBytes)
	return fmt.Sprintf("iniciado em segundo plano: job_id=%s (use wait para coletar a saída)", job.ID), nil
}

// Wait aguarda um job iniciado por run_shell_bg terminar e devolve a saída
// acumulada. É somente leitura: não exige aprovação.
type Wait struct {
	Jobs *Jobs
}

func (Wait) Name() string { return "wait" }
func (Wait) Description() string {
	return "Aguarda um comando iniciado por run_shell_bg terminar (até 10s por chamada; repita para continuar esperando) e devolve a saída."
}

func (Wait) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"job_id":  map[string]any{"type": "string", "description": "identificador devolvido por run_shell_bg"},
			"timeout": map[string]any{"type": "string", "description": "espera máxima dentro da chamada, ex.: \"5s\" (vazio = até terminar)"},
		},
		"required": []string{"job_id"},
	}
}

func (t Wait) Run(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		JobID   string `json:"job_id"`
		Timeout string `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumentos inválidos: %w", err)
	}
	if strings.TrimSpace(in.JobID) == "" {
		return "", errors.New(`argumento "job_id" é obrigatório`)
	}
	var timeout time.Duration
	if s := strings.TrimSpace(in.Timeout); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return "", fmt.Errorf("timeout inválido: %w", err)
		}
		timeout = d
	}
	if t.Jobs == nil {
		return "", errors.New("registro de jobs indisponível")
	}
	return t.Jobs.Wait(ctx, in.JobID, timeout)
}
