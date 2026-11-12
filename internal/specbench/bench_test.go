package specbench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseAcceptance(t *testing.T) {
	const line = "draft acceptance = 0.43827 (   71 accepted /   162 generated), mean len =  2.29"
	a, ok := ParseAcceptance(line)
	if !ok {
		t.Fatalf("esperava reconhecer a linha de aceitação")
	}
	if a.Accepted != 71 || a.Generated != 162 {
		t.Fatalf("accepted/generated = %d/%d, esperava 71/162", a.Accepted, a.Generated)
	}
	if a.Rate < 0.4382 || a.Rate > 0.4383 {
		t.Fatalf("rate = %v, esperava ~0.43827", a.Rate)
	}
	if a.MeanLen < 2.289 || a.MeanLen > 2.291 {
		t.Fatalf("mean len = %v, esperava ~2.29", a.MeanLen)
	}
}

func TestParseAcceptanceSemDraft(t *testing.T) {
	if _, ok := ParseAcceptance("nop"); ok {
		t.Fatalf("não deveria reconhecer texto sem estatística de draft")
	}
}

func argValue(args []string, flag string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1], true
		}
	}
	return "", false
}

func TestCaseArgsComDraft(t *testing.T) {
	c := Case{
		Target:       "/m/t.gguf",
		Draft:        "/m/d.gguf",
		SpecType:     "draft-simple",
		Threads:      10,
		ThreadsDraft: 8,
		NMax:         3,
		CtxSize:      2048,
		Port:         8123,
	}
	args := c.args()
	want := map[string]string{
		"-m":                 "/m/t.gguf",
		"-md":                "/m/d.gguf",
		"--spec-type":        "draft-simple",
		"-td":                "8",
		"--spec-draft-n-max": "3",
		"-t":                 "10",
		"-c":                 "2048",
		"--port":             "8123",
	}
	for flag, w := range want {
		got, ok := argValue(args, flag)
		if !ok || got != w {
			t.Errorf("flag %s = %q (presente=%v), esperava %q", flag, got, ok, w)
		}
	}
}

func TestCaseArgsSemDraft(t *testing.T) {
	c := Case{Target: "/m/t.gguf", Threads: 10, CtxSize: 2048, Port: 8123}
	args := c.args()
	if _, ok := argValue(args, "--spec-type"); ok {
		t.Errorf("caso base não deveria conter --spec-type: %v", args)
	}
	if _, ok := argValue(args, "-md"); ok {
		t.Errorf("caso base não deveria conter -md: %v", args)
	}
}

func TestWithDefaults(t *testing.T) {
	c := Case{Target: "/m/t.gguf", Draft: "/m/d.gguf"}.withDefaults()
	if c.SpecType != "draft-simple" {
		t.Errorf("SpecType = %q, esperava draft-simple", c.SpecType)
	}
	if c.Threads != 10 || c.CtxSize != 2048 || c.NPredict != 128 || c.Port != 8123 {
		t.Errorf("defaults inesperados: %+v", c)
	}
	if c.LogPath == "" {
		t.Errorf("LogPath deveria ter um padrão")
	}
}

func TestReadLastAcceptance(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "srv.log")
	content := "linha qualquer\n" +
		"draft acceptance = 0.39286 (   77 accepted /   196 generated), mean len =  2.57\n" +
		"draft acceptance = 0.43827 (   71 accepted /   162 generated), mean len =  2.29\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("escrever log: %v", err)
	}
	a, ok := readLastAcceptance(path)
	if !ok {
		t.Fatalf("esperava encontrar a linha de aceitação")
	}
	if a.Accepted != 71 || a.MeanLen < 2.289 || a.MeanLen > 2.291 {
		t.Fatalf("esperava a ÚLTIMA linha (71 accepted, 2.29), obteve %+v", a)
	}
}
