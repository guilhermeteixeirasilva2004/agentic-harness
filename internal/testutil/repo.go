package testutil

import (
	"encoding/json"
	"sync"
	"time"

	"agentic-harness/internal/store"
)

// FakeRepository é um repositório de sessões em memória, com a mesma forma do
// store.Store (Append/Query/Path). Serve para testar casos de uso sem tocar no
// disco e permite simular falhas de escrita.
type FakeRepository struct {
	mu         sync.Mutex
	records    []store.Record
	failAppend error
}

// NewFakeRepository cria um repositório vazio em memória.
func NewFakeRepository() *FakeRepository { return &FakeRepository{} }

// Path devolve um rótulo fixo (não há arquivo).
func (r *FakeRepository) Path() string { return "(memória)" }

// Append grava um registro, serializando o payload como JSON.
func (r *FakeRepository) Append(kind, session string, payload any) error {
	var raw json.RawMessage
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = b
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failAppend != nil {
		return r.failAppend
	}
	r.records = append(r.records, store.Record{
		Type:    kind,
		Session: session,
		At:      time.Now(),
		Payload: raw,
	})
	return nil
}

// Query devolve uma cópia dos registros gravados.
func (r *FakeRepository) Query() ([]store.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]store.Record(nil), r.records...), nil
}

// Records devolve os registros gravados (atalho para Query em texto).
func (r *FakeRepository) Records() []store.Record {
	recs, _ := r.Query()
	return recs
}

// Payloads devolve os payloads dos registros do tipo informado.
func (r *FakeRepository) Payloads(kind string) []json.RawMessage {
	recs, _ := r.Query()
	var out []json.RawMessage
	for _, rec := range recs {
		if rec.Type == kind {
			out = append(out, rec.Payload)
		}
	}
	return out
}

// FailAppend faz o próximo Append falhar com err (nil volta ao normal).
func (r *FakeRepository) FailAppend(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failAppend = err
}

// Reset descarta tudo, mantendo a falha configurada.
func (r *FakeRepository) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = nil
}
