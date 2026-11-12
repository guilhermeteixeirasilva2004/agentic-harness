package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// jobIDFrom extrai o identificador de um job da resposta de run_shell_bg.
func jobIDFrom(t *testing.T, out string) string {
	t.Helper()
	const marker = "job_id="
	i := strings.Index(out, marker)
	if i < 0 {
		t.Fatalf("resposta sem job_id: %q", out)
	}
	rest := out[i+len(marker):]
	if j := strings.IndexAny(rest, " )"); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		t.Fatalf("job_id vazio em %q", out)
	}
	return rest
}

func TestRunShellBGEWaitColetaSaida(t *testing.T) {
	jobs := NewJobs()
	bg := RunShellBG{Root: t.TempDir(), Allow: []string{"echo"}, Jobs: jobs}

	out, err := bg.Run(context.Background(), json.RawMessage(`{"command":"echo oi mundo"}`))
	if err != nil {
		t.Fatalf("run_shell_bg: %v", err)
	}
	id := jobIDFrom(t, out)

	got, err := Wait{Jobs: jobs}.Run(context.Background(), json.RawMessage(`{"job_id":"`+id+`"}`))
	if err != nil {
		t.Fatalf("wait: %v (saída %q)", err, got)
	}
	if !strings.Contains(got, "oi mundo") {
		t.Fatalf("saída do job = %q", got)
	}
}

func TestWaitTimeoutNaoBloqueia(t *testing.T) {
	jobs := NewJobs()
	bg := RunShellBG{Root: t.TempDir(), Allow: []string{"sleep"}, Jobs: jobs}

	out, err := bg.Run(context.Background(), json.RawMessage(`{"command":"sleep 0.5"}`))
	if err != nil {
		t.Fatalf("run_shell_bg: %v", err)
	}
	id := jobIDFrom(t, out)

	started := time.Now()
	if _, err := (Wait{Jobs: jobs}).Run(context.Background(), json.RawMessage(`{"job_id":"`+id+`","timeout":"50ms"}`)); err == nil {
		t.Fatal("esperava erro de timeout do wait")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("wait ignorou o timeout: %s", elapsed)
	}
	// Drena o job para não deixar processo órfão ao fim do teste.
	if _, err := (Wait{Jobs: jobs}).Run(context.Background(), json.RawMessage(`{"job_id":"`+id+`","timeout":"5s"}`)); err != nil {
		t.Fatalf("drenagem do job falhou: %v", err)
	}
}

func TestRunShellBGRespeitaAllowlistEMetacaracteres(t *testing.T) {
	jobs := NewJobs()
	bg := RunShellBG{Root: t.TempDir(), Allow: []string{"echo"}, Jobs: jobs}

	for _, cmd := range []string{`{"command":"rm -rf /"}`, `{"command":"echo a; echo b"}`} {
		if _, err := bg.Run(context.Background(), json.RawMessage(cmd)); err == nil {
			t.Fatalf("esperava bloqueio para %s", cmd)
		}
	}
}

func TestRunShellBGSemComando(t *testing.T) {
	bg := RunShellBG{Root: t.TempDir(), Allow: []string{"echo"}, Jobs: NewJobs()}
	if _, err := bg.Run(context.Background(), json.RawMessage(`{"command":"  "}`)); err == nil {
		t.Fatal("esperava erro para comando vazio")
	}
}

func TestWaitJobDesconhecido(t *testing.T) {
	if _, err := (Wait{Jobs: NewJobs()}).Run(context.Background(), json.RawMessage(`{"job_id":"job-999"}`)); err == nil {
		t.Fatal("esperava erro para job desconhecido")
	}
}

func TestAprovacaoDasToolsAssincronas(t *testing.T) {
	if !RequiresApproval(RunShellBG{}) {
		t.Fatal("run_shell_bg deveria exigir aprovação")
	}
	if RequiresApproval(Wait{}) {
		t.Fatal("wait é somente leitura e não deveria exigir aprovação")
	}
}
