package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentic-harness/internal/cognition"
	"agentic-harness/internal/llm"
	"agentic-harness/internal/reasoning"
	"agentic-harness/internal/store"
)

// continuePrompt é a instrução enviada ao retomar uma tarefa incompleta salva
// em checkpoint. O histórico (com as observações já coletadas) é restaurado
// antes, então o agente prossegue sem repetir o trabalho feito.
const continuePrompt = "Continue a tarefa anterior de onde parou, sem repetir o que já foi feito. " +
	"Use as observações já disponíveis no histórico e conclua com action \"final\" assim que possível."

// checkpointRecord é o "save state" da tarefa em andamento: histórico completo
// (incluindo observações), memória, notas, plano e estratégia. É gravado quando
// os passos acabam sem resposta final, permitindo retomar em outro processo
// (-continue), onde a memória do processo já não existe.
type checkpointRecord struct {
	Input    string        `json:"input"`
	History  []llm.Message `json:"history"`
	Memory   []string      `json:"memory,omitempty"`
	Notes    []string      `json:"notes,omitempty"`
	Plan     []string      `json:"plan,omitempty"`
	Strategy string        `json:"strategy"`
	Model    string        `json:"model"`
}

// persistCheckpoint grava o estado atual da tarefa no store, se houver um
// conectado. Falhas são apenas registradas no trace (não interrompem o turno).
func (a *Agent) persistCheckpoint(input string) {
	if a.store == nil {
		return
	}
	rec := checkpointRecord{
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

// pendingCheckpoint devolve o checkpoint mais recente ainda não concluído por um
// turno bem-sucedido. explicit indica sessão fixada; sem isso, usa o último
// checkpoint gravado (qualquer sessão). Um turno que terminou com erro não
// consome o checkpoint (a tarefa segue incompleta).
func pendingCheckpoint(recs []store.Record, session string, explicit bool) (checkpointRecord, string, bool) {
	idx := -1
	var cp checkpointRecord
	var cpSession string
	for i, r := range recs {
		switch r.Type {
		case "checkpoint":
			if explicit && r.Session != session {
				continue
			}
			var c checkpointRecord
			if err := json.Unmarshal(r.Payload, &c); err != nil {
				continue
			}
			cp, cpSession, idx = c, r.Session, i
		case "turn":
			if idx < 0 || r.Session != cpSession {
				continue
			}
			var t turnRecord
			if err := json.Unmarshal(r.Payload, &t); err == nil && strings.TrimSpace(t.Error) == "" {
				// Conclusão bem-sucedida depois do checkpoint.
				idx = -1
			}
		}
	}
	if idx < 0 {
		return checkpointRecord{}, "", false
	}
	return cp, cpSession, true
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
	_, _, ok := pendingCheckpoint(recs, a.session, a.sessionExplicit)
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
	cp, cpSession, ok := pendingCheckpoint(recs, a.session, a.sessionExplicit)
	if !ok {
		return "", fmt.Errorf("nenhum checkpoint pendente para continuar")
	}
	if !a.sessionExplicit && strings.TrimSpace(cpSession) != "" {
		a.session = cpSession
	}
	a.restoreCheckpoint(cp)
	return a.Turn(ctx, continuePrompt, onDelta)
}

// ResumeTask é o caso de uso de /continue: retoma a tarefa anterior. Com
// checkpoint pendente (turno interrompido pelo limite de passos) restaura o
// estado salvo; sem checkpoint, reaproveita o histórico em memória. A UI não
// precisa conhecer o prompt nem saber que existe checkpoint.
func (a *Agent) ResumeTask(ctx context.Context, onDelta func(string)) (string, error) {
	if a.HasCheckpoint() {
		return a.Continue(ctx, onDelta)
	}
	return a.Turn(ctx, continuePrompt, onDelta)
}

// restoreCheckpoint reconstrói o estado da tarefa a partir do checkpoint.
func (a *Agent) restoreCheckpoint(cp checkpointRecord) {
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
