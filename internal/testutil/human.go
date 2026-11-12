package testutil

import (
	"context"
	"sync"
)

// ApproverFunc adapta uma função ao contrato de aprovação do agente. O tipo é
// genérico no request de propósito: este pacote não importa agent (importá-lo
// aqui criaria um ciclo nos testes internos do agente) e a instância com
// Req=agent.ApprovalRequest satisfaz a interface agent.Approver.
type ApproverFunc[Req any] func(ctx context.Context, req Req) (bool, error)

// Approve chama a função adaptada.
func (f ApproverFunc[Req]) Approve(ctx context.Context, req Req) (bool, error) {
	return f(ctx, req)
}

// Approver é um aprovador roteirizado: responde sempre Allow (ou Err) e grava
// as solicitações recebidas, para que os testes possam inspecioná-las.
type Approver[Req any] struct {
	mu       sync.Mutex
	allow    bool
	err      error
	requests []Req
}

// NewApprover cria um aprovador que responde allow a todas as solicitações.
func NewApprover[Req any](allow bool) *Approver[Req] {
	return &Approver[Req]{allow: allow}
}

// Approve registra a solicitação e devolve a resposta configurada.
func (a *Approver[Req]) Approve(ctx context.Context, req Req) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, req)
	return a.allow, a.err
}

// SetAnswer troca a resposta (e o erro) devolvidos adiante.
func (a *Approver[Req]) SetAnswer(allow bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.allow, a.err = allow, err
}

// Requests devolve as solicitações recebidas.
func (a *Approver[Req]) Requests() []Req {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Req(nil), a.requests...)
}

// Count devolve quantas solicitações foram feitas.
func (a *Approver[Req]) Count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.requests)
}

// AskerFunc adapta uma função ao contrato agent.Asker.
type AskerFunc func(ctx context.Context, question string) (string, error)

// Ask chama a função adaptada.
func (f AskerFunc) Ask(ctx context.Context, question string) (string, error) {
	return f(ctx, question)
}

// Asker é um interlocutor roteirizado: devolve as respostas em ordem e grava as
// perguntas feitas.
type Asker struct {
	mu        sync.Mutex
	answers   []string
	err       error
	questions []string
}

// NewAsker cria um interlocutor com as respostas informadas na fila.
func NewAsker(answers ...string) *Asker { return &Asker{answers: answers} }

// Ask registra a pergunta e devolve a próxima resposta (ou ErrNoReply, se a
// fila acabou).
func (a *Asker) Ask(ctx context.Context, question string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.questions = append(a.questions, question)
	if a.err != nil {
		return "", a.err
	}
	if len(a.answers) == 0 {
		return "", ErrNoReply
	}
	next := a.answers[0]
	a.answers = a.answers[1:]
	return next, nil
}

// SetErr faz todas as próximas perguntas falharem com err.
func (a *Asker) SetErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.err = err
}

// Questions devolve as perguntas recebidas.
func (a *Asker) Questions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.questions...)
}
