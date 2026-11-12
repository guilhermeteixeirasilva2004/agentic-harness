package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentic-harness/internal/config"
	"agentic-harness/internal/safety"
	"agentic-harness/internal/store"
	"agentic-harness/internal/tools"
)

// fakeApprover registra os pedidos e responde sempre com o mesmo veredito.
type fakeApprover struct {
	allow bool
	calls []ApprovalRequest
}

func (f *fakeApprover) Approve(_ context.Context, req ApprovalRequest) (bool, error) {
	f.calls = append(f.calls, req)
	return f.allow, nil
}

const writeCall = `{"action":"tool","tool":"write_file","arguments":{"path":"a.txt","content":"oi"}}`

func newWriteFileAgent(t *testing.T, f *fakeLLM, ap Approver) (*Agent, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{System: "teste", MaxSteps: 4}
	reg := tools.NewRegistry()
	reg.Add(tools.WriteFile{Root: dir})
	reg.Allow("write_file")
	a := New(cfg, f, reg, safety.New(4, []string{"write_file"}))
	if ap != nil {
		a.SetApprover(ap)
	}
	return a, dir
}

func TestApprovalPermiteToolMutante(t *testing.T) {
	f := &fakeLLM{responses: []string{writeCall, `{"action":"final","answer":"pronto"}`}}
	ap := &fakeApprover{allow: true}
	a, dir := newWriteFileAgent(t, f, ap)

	out, err := a.Turn(context.Background(), "grave a.txt", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "pronto" {
		t.Fatalf("out = %q", out)
	}
	if len(ap.calls) != 1 {
		t.Fatalf("pedidos de aprovação = %d, esperava 1", len(ap.calls))
	}
	if ap.calls[0].Tool != "write_file" || !strings.Contains(ap.calls[0].Summary, "a.txt") {
		t.Fatalf("pedido inesperado: %+v", ap.calls[0])
	}
	if data, err := os.ReadFile(filepath.Join(dir, "a.txt")); err != nil || string(data) != "oi" {
		t.Fatalf("arquivo não gravado: %q err=%v", data, err)
	}
	s := a.meter.Snapshot()
	if s.Approvals != 1 || s.Denials != 0 {
		t.Fatalf("métricas = %d/%d, esperava 1/0", s.Approvals, s.Denials)
	}
	if !strings.Contains(f.requests[1].Messages[len(f.requests[1].Messages)-1].Content, "OBSERVAÇÃO") {
		t.Fatalf("observação não injetada: %+v", f.requests[1].Messages)
	}
}

func TestApprovalNegaToolMutante(t *testing.T) {
	f := &fakeLLM{responses: []string{writeCall, `{"action":"final","answer":"ok"}`}}
	ap := &fakeApprover{allow: false}
	a, dir := newWriteFileAgent(t, f, ap)

	out, err := a.Turn(context.Background(), "grave a.txt", nil)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if out != "ok" {
		t.Fatalf("out = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("arquivo não deveria ter sido criado após negação")
	}
	if s := a.meter.Snapshot(); s.Denials != 1 || s.Approvals != 0 {
		t.Fatalf("métricas = %d/%d, esperava 0/1", s.Approvals, s.Denials)
	}
	last := f.requests[1].Messages[len(f.requests[1].Messages)-1].Content
	if !strings.Contains(last, "APROVAÇÃO NEGADA (write_file)") {
		t.Fatalf("feedback de negação ausente: %+v", f.requests[1].Messages)
	}
	if len(a.steps.Steps) == 0 {
		t.Fatal("trace vazio")
	}
}

func TestApprovalSemApproverNega(t *testing.T) {
	f := &fakeLLM{responses: []string{writeCall, `{"action":"final","answer":"ok"}`}}
	a, dir := newWriteFileAgent(t, f, nil)

	if _, err := a.Turn(context.Background(), "grave a.txt", nil); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("sem approver, a tool mutante deveria ser negada")
	}
	if s := a.meter.Snapshot(); s.Denials != 1 {
		t.Fatalf("negações = %d, esperava 1", s.Denials)
	}
}

func TestApprovalNaoPedeParaToolSomenteLeitura(t *testing.T) {
	f := &fakeLLM{responses: []string{
		`{"action":"tool","tool":"get_time","arguments":{}}`,
		`{"action":"final","answer":"ok"}`,
	}}
	ap := &fakeApprover{allow: false}
	a := newTestAgent(f, 4)
	a.SetApprover(ap)

	if _, err := a.Turn(context.Background(), "que horas são?", nil); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(ap.calls) != 0 {
		t.Fatalf("get_time não deveria pedir aprovação: %+v", ap.calls)
	}
}

func TestApprovalAuditaNoStore(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	f := &fakeLLM{responses: []string{writeCall, `{"action":"final","answer":"ok"}`}}
	a, _ := newWriteFileAgent(t, f, &fakeApprover{allow: true})
	a.SetSession("sess-teste")
	a.SetStore(st)

	if _, err := a.Turn(context.Background(), "grave a.txt", nil); err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	recs, err := st.Query()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range recs {
		if r.Type != "approval" || r.Session != "sess-teste" {
			continue
		}
		var rec approvalRecord
		if err := json.Unmarshal(r.Payload, &rec); err != nil {
			t.Fatalf("payload inválido: %v", err)
		}
		if rec.Tool != "write_file" || !rec.Approved {
			t.Fatalf("registro de auditoria inesperado: %+v", rec)
		}
		if !strings.Contains(rec.Summary, "write_file") || !strings.Contains(rec.Summary, "a.txt") {
			t.Fatalf("o resumo da ação deveria estar auditado: %+v", rec)
		}
		found = true
	}
	if !found {
		t.Fatalf("nenhum registro de aprovação no store: %+v", recs)
	}
}
