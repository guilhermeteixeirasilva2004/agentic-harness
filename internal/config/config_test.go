package config

import (
	"testing"
	"time"
)

func TestLoadAllowInsecureHTTP(t *testing.T) {
	t.Setenv("HARNESS_ALLOW_INSECURE_HTTP", "")

	if cfg := Load(nil); cfg.AllowInsecureHTTP {
		t.Fatal("padrão deveria recusar http:// remoto")
	}
	if cfg := Load([]string{"-allow-insecure-http"}); !cfg.AllowInsecureHTTP {
		t.Fatal("-allow-insecure-http não foi aplicado")
	}

	t.Setenv("HARNESS_ALLOW_INSECURE_HTTP", "true")
	if cfg := Load(nil); !cfg.AllowInsecureHTTP {
		t.Fatal("HARNESS_ALLOW_INSECURE_HTTP=true deveria ligar a opção")
	}
}

func TestLoadRAGTopKRetriesPeloEnv(t *testing.T) {
	t.Setenv("HARNESS_RAG", "true")
	t.Setenv("HARNESS_TOPK", "3")
	t.Setenv("HARNESS_RETRIES", "4")
	t.Setenv("HARNESS_BACKOFF", "250ms")

	cfg := Load(nil)
	if !cfg.RAG {
		t.Fatal("HARNESS_RAG=true deveria ligar o RAG")
	}
	if cfg.TopK != 3 {
		t.Fatalf("HARNESS_TOPK => %d, esperava 3", cfg.TopK)
	}
	if cfg.Retries != 4 {
		t.Fatalf("HARNESS_RETRIES => %d, esperava 4", cfg.Retries)
	}
	if cfg.Backoff != 250*time.Millisecond {
		t.Fatalf("HARNESS_BACKOFF => %v, esperava 250ms", cfg.Backoff)
	}
}

func TestLoadRAGTopKRetriesViaFlag(t *testing.T) {
	t.Setenv("HARNESS_RAG", "")
	t.Setenv("HARNESS_TOPK", "")
	t.Setenv("HARNESS_RETRIES", "")
	t.Setenv("HARNESS_BACKOFF", "")

	cfg := Load([]string{"-rag", "-topk", "2", "-retries", "0", "-backoff", "1s"})
	if !cfg.RAG || cfg.TopK != 2 || cfg.Retries != 0 || cfg.Backoff != time.Second {
		t.Fatalf("cfg = %+v", cfg)
	}

	// Padrões seguros: RAG desligado, topk 5, 2 retentativas, backoff 500ms.
	def := Load(nil)
	if def.RAG || def.TopK != 5 || def.Retries != 2 || def.Backoff != 500*time.Millisecond {
		t.Fatalf("padrões = %+v", def)
	}
}

func TestLoadNormalizaValoresInvalidos(t *testing.T) {
	t.Setenv("HARNESS_RAG", "")
	t.Setenv("HARNESS_TOPK", "")
	t.Setenv("HARNESS_RETRIES", "")
	t.Setenv("HARNESS_BACKOFF", "")

	cfg := Load([]string{"-topk", "-3", "-retries", "-1", "-backoff", "0s"})
	if cfg.TopK != 5 {
		t.Fatalf("-topk negativo => %d, esperava 5", cfg.TopK)
	}
	if cfg.Retries != 0 {
		t.Fatalf("-retries negativo => %d, esperava 0", cfg.Retries)
	}
	if cfg.Backoff != 500*time.Millisecond {
		t.Fatalf("-backoff 0 => %v, esperava 500ms", cfg.Backoff)
	}
}

func TestLoadStrategy(t *testing.T) {
	t.Setenv("HARNESS_STRATEGY", "")
	t.Setenv("HARNESS_MAXCONTEXT", "")

	if got := Load(nil).Strategy; got != "react" {
		t.Fatalf("estratégia padrão = %q, esperava react", got)
	}
	if got := Load([]string{"-strategy", "cot"}).Strategy; got != "cot" {
		t.Fatalf("-strategy cot => %q", got)
	}
	cfg := Load([]string{"-strategy", "plan", "-maxcontext", "2048"})
	if cfg.Strategy != "plan" {
		t.Fatalf("-strategy plan => %q", cfg.Strategy)
	}
	if cfg.MaxContextTokens != 2048 {
		t.Fatalf("-maxcontext => %d, esperava 2048", cfg.MaxContextTokens)
	}
}

func TestLoadStrategyViaEnv(t *testing.T) {
	t.Setenv("HARNESS_STRATEGY", "COT")
	if got := Load(nil).Strategy; got != "cot" {
		t.Fatalf("HARNESS_STRATEGY=COT => %q, esperava cot", got)
	}
}

func TestLoadPersistencia(t *testing.T) {
	t.Setenv("HARNESS_STORE", "")
	t.Setenv("HARNESS_SESSION", "")
	t.Setenv("HARNESS_RESUME", "")

	cfg := Load([]string{"-store", "/tmp/x.jsonl", "-session", "abc", "-resume"})
	if cfg.StorePath != "/tmp/x.jsonl" {
		t.Fatalf("-store => %q", cfg.StorePath)
	}
	if cfg.Session != "abc" {
		t.Fatalf("-session => %q", cfg.Session)
	}
	if !cfg.Resume {
		t.Fatal("-resume deveria ativar a retomada")
	}
	if Load(nil).Resume {
		t.Fatal("resume deveria ser false por padrão")
	}
}

func TestLoadPersistenciaViaEnv(t *testing.T) {
	t.Setenv("HARNESS_STORE", "/tmp/env.jsonl")
	t.Setenv("HARNESS_SESSION", "env-sess")
	t.Setenv("HARNESS_RESUME", "true")

	cfg := Load(nil)
	if cfg.StorePath != "/tmp/env.jsonl" || cfg.Session != "env-sess" || !cfg.Resume {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadContinue(t *testing.T) {
	t.Setenv("HARNESS_CONTINUE", "")

	if Load(nil).Continue {
		t.Fatal("-continue deveria ser false por padrão")
	}
	if !Load([]string{"-continue"}).Continue {
		t.Fatal("-continue deveria ativar a retomada do checkpoint")
	}
}

func TestLoadContinueViaEnv(t *testing.T) {
	t.Setenv("HARNESS_CONTINUE", "true")
	if !Load(nil).Continue {
		t.Fatal("HARNESS_CONTINUE=true deveria ativar a retomada do checkpoint")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	if got := DefaultPath(); got != "/tmp/xdg/agentic-harness/sessions.jsonl" {
		t.Fatalf("DefaultPath = %q", got)
	}
}

func TestLoadApproveEShellAllow(t *testing.T) {
	t.Setenv("HARNESS_APPROVE", "")
	t.Setenv("HARNESS_SHELLALLOW", "")

	if got := Load(nil).Approve; got != "ask" {
		t.Fatalf("aprovação padrão = %q, esperava ask", got)
	}
	cfg := Load([]string{"-approve", "AUTO", "-shellallow", "ls, git status"})
	if cfg.Approve != "auto" {
		t.Fatalf("-approve AUTO => %q", cfg.Approve)
	}
	if len(cfg.ShellAllow) != 2 || cfg.ShellAllow[1] != "git status" {
		t.Fatalf("-shellallow => %#v", cfg.ShellAllow)
	}
	if got := Load([]string{"-approve", "talvez"}).Approve; got != "ask" {
		t.Fatalf("modo inválido deveria cair em ask, veio %q", got)
	}
}

func TestLoadApproveViaEnv(t *testing.T) {
	t.Setenv("HARNESS_APPROVE", "deny")
	t.Setenv("HARNESS_SHELLALLOW", "ls,cat")

	cfg := Load(nil)
	if cfg.Approve != "deny" {
		t.Fatalf("HARNESS_APPROVE=deny => %q", cfg.Approve)
	}
	if len(cfg.ShellAllow) != 2 || cfg.ShellAllow[0] != "ls" || cfg.ShellAllow[1] != "cat" {
		t.Fatalf("HARNESS_SHELLALLOW => %#v", cfg.ShellAllow)
	}
}

func TestNormalizeApprove(t *testing.T) {
	cases := map[string]string{
		"ASK": "ask", "ask": "ask", "": "ask", "qualquer": "ask",
		"auto": "auto", "AUTO": "auto", "deny": "deny", " Deny ": "deny",
	}
	for in, want := range cases {
		if got := NormalizeApprove(in); got != want {
			t.Fatalf("NormalizeApprove(%q) = %q, esperava %q", in, got, want)
		}
	}
}
