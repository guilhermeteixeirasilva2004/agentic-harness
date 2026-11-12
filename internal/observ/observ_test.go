package observ

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMeterAcumula(t *testing.T) {
	m := New()
	m.RecordTurn(120 * time.Millisecond)
	m.RecordStep()
	m.RecordStep()
	m.RecordLLM(50 * time.Millisecond)
	m.RecordTokens(100, 20)
	m.RecordTool("read_file", 5*time.Millisecond, nil)
	m.RecordTool("read_file", 7*time.Millisecond, errors.New("falhou"))

	s := m.Snapshot()
	if s.Turns != 1 || s.Steps != 2 || s.LLMCalls != 1 {
		t.Fatalf("contadores = %+v", s)
	}
	if s.TokensIn != 100 || s.TokensOut != 20 {
		t.Fatalf("tokens = %d/%d, esperava 100/20", s.TokensIn, s.TokensOut)
	}
	if s.ToolCalls != 2 || s.ToolErrors != 1 {
		t.Fatalf("tools = %d chamadas, %d erros", s.ToolCalls, s.ToolErrors)
	}
	if s.LLMTotal != 50*time.Millisecond || s.ToolTotal != 12*time.Millisecond {
		t.Fatalf("latências = %s/%s", s.LLMTotal, s.ToolTotal)
	}
	st := s.PerTool["read_file"]
	if st.Calls != 2 || st.Errors != 1 || st.Total != 12*time.Millisecond {
		t.Fatalf("stat da tool = %+v", st)
	}
}

func TestMeterToolSemNome(t *testing.T) {
	m := New()
	m.RecordTool("  ", time.Millisecond, nil)
	if _, ok := m.Snapshot().PerTool["(desconhecida)"]; !ok {
		t.Fatalf("nome vazio deveria virar (desconhecida): %+v", m.Snapshot().PerTool)
	}
}

func TestSummary(t *testing.T) {
	m := New()
	m.RecordTurn(time.Second)
	m.RecordLLM(100 * time.Millisecond)
	m.RecordTool("get_time", time.Millisecond, nil)

	out := m.Snapshot().Summary()
	for _, want := range []string{"métricas da sessão", "turnos:       1", "get_time"} {
		if !strings.Contains(out, want) {
			t.Fatalf("resumo sem %q:\n%s", want, out)
		}
	}
}

func TestSnapshotIsolado(t *testing.T) {
	m := New()
	m.RecordTool("a", time.Millisecond, nil)

	snap := m.Snapshot()
	snap.PerTool["a"] = ToolStat{}
	delete(snap.PerTool, "a")
	snap.Turns = 99

	again := m.Snapshot()
	if _, ok := again.PerTool["a"]; !ok {
		t.Fatal("alterar o snapshot não deveria afetar o meter")
	}
	if again.Turns != 0 {
		t.Fatalf("Turns = %d, esperava 0", again.Turns)
	}
}

func TestMeterAprovacoes(t *testing.T) {
	m := New()
	m.RecordApproval()
	m.RecordApproval()
	m.RecordDenial()

	s := m.Snapshot()
	if s.Approvals != 2 || s.Denials != 1 {
		t.Fatalf("aprovações = %d/%d, esperava 2/1", s.Approvals, s.Denials)
	}
	out := s.Summary()
	if !strings.Contains(out, "2 aprovada(s), 1 negada(s)") {
		t.Fatalf("resumo sem contagem de aprovações:\n%s", out)
	}
}

func TestMeterConcorrente(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.RecordStep()
			m.RecordLLM(time.Millisecond)
			m.RecordTokens(1, 1)
			m.RecordTool("x", time.Millisecond, nil)
		}()
	}
	wg.Wait()

	s := m.Snapshot()
	if s.Steps != 50 || s.LLMCalls != 50 || s.ToolCalls != 50 || s.TokensIn != 50 {
		t.Fatalf("agregação concorrente inconsistente: %+v", s)
	}
}
