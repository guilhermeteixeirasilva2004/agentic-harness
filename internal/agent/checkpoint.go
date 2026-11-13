package agent

import (
	"context"
	"fmt"
	"strings"

	"agentic-harness/internal/checkpoint"
	"agentic-harness/internal/cognition"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/reasoning"
)

// persistCheckpoint grava o estado atual da tarefa no store, se houver um
// conectado. Falhas são apenas registradas no trace (não interrompem o turno).
func (a *Agent) persistCheckpoint(input string) {
	if a.store == nil {
		return
	}
	rec := checkpoint.Record{
		Input:    input,
		History:  append([]llm.Message{}, a.history...),
		Memory:   append([]string{}, a.memory...),
		Notes:    append([]string{}, a.notes...),
		Plan:     a.steps.Plan(),
		Strategy: a.strategy.Name(),
		Model:    a.cfg.Model,
	}
	if err := a.store.Append("checkpoint", a.session, rec); err != nil {
		a.logf("falha ao gravar checkpoint: %v", err)
	}
}

// HasCheckpoint informa se há uma tarefa incompleta salva em checkpoint.
func (a *Agent) HasCheckpoint() bool {
	if a.store == nil {
		return false
	}
	recs, err := a.store.Query()
	if err != nil {
		return false
	}
	_, _, ok := checkpoint.Pending(recs, a.session, a.sessionExplicit)
	return ok
}

// Continue restaura a tarefa incompleta salva em checkpoint e roda o loop a
// partir dela. Exige um store conectado e um checkpoint pendente.
func (a *Agent) Continue(ctx context.Context, onDelta func(string)) (string, error) {
	if a.store == nil {
		return "", fmt.Errorf("persistência desativada")
	}
	recs, err := a.store.Query()
	if err != nil {
		return "", err
	}
	cp, cpSession, ok := checkpoint.Pending(recs, a.session, a.sessionExplicit)
	if !ok {
		return "", fmt.Errorf("nenhum checkpoint pendente para continuar")
	}
	if !a.sessionExplicit && strings.TrimSpace(cpSession) != "" {
		a.session = cpSession
	}
	a.restoreCheckpoint(cp)
	return a.Turn(ctx, checkpoint.ContinuePrompt, onDelta)
}

// ResumeTask é o caso de uso de /continue: retoma a tarefa anterior. Com
// checkpoint pendente (turno interrompido pelo limite de passos) restaura o
// estado salvo; sem checkpoint, reaproveita o histórico em memória. A UI não
// precisa conhecer o prompt nem saber que existe checkpoint.
func (a *Agent) ResumeTask(ctx context.Context, onDelta func(string)) (string, error) {
	if a.HasCheckpoint() {
		return a.Continue(ctx, onDelta)
	}
	return a.Turn(ctx, checkpoint.ContinuePrompt, onDelta)
}

// restoreCheckpoint reconstrói o estado da tarefa a partir do checkpoint.
func (a *Agent) restoreCheckpoint(cp checkpoint.Record) {
	if len(cp.History) > 0 {
		a.history = append([]llm.Message{}, cp.History...)
	} else {
		a.rebuild()
	}
	a.memory = append([]string{}, cp.Memory...)
	if len(cp.Notes) > 0 {
		a.notes = append([]string{}, cp.Notes...)
	}
	a.selected = nil
	if cp.Strategy != "" {
		a.strategy = reasoning.Get(cp.Strategy)
	}
	a.steps = cognition.New(newTraceID())
	if len(cp.Plan) > 0 {
		a.steps.Append(cognition.Step{Kind: cognition.KindPlan, Plan: cp.Plan})
	}
	a.refreshMemory()
}
