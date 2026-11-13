package memory

import (
	"math"
	"strings"
	"testing"

	"agentic-harness/internal/llm"
)

func TestLine(t *testing.T) {
	cases := []struct {
		msg  llm.Message
		want string
	}{
		{llm.Message{Role: llm.RoleUser, Content: "oi"}, "- você: oi"},
		{llm.Message{Role: llm.RoleAssistant, Content: "olá"}, "- assistente: olá"},
		{llm.Message{Role: llm.RoleUser, Content: "ERRO (x): falhou"}, "- harness: ERRO (x): falhou"},
		{llm.Message{Role: llm.RoleUser, Content: "OBSERVAÇÃO (read_file): conteúdo"}, "- harness: OBSERVAÇÃO (read_file): conteúdo"},
		{llm.Message{Role: llm.RoleSystem, Content: "sistema"}, ""},
		{llm.Message{Role: llm.RoleUser, Content: "  \n "}, ""},
	}
	for _, tc := range cases {
		if got := Line(tc.msg); got != tc.want {
			t.Fatalf("Line(%+v) = %q, esperava %q", tc.msg, got, tc.want)
		}
	}

	long := Line(llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 300)})
	if n := len([]rune(long)); n > lineChars+10 {
		t.Fatalf("linha não truncada (%d runas): %q", n, long)
	}
	if !strings.HasSuffix(long, "…") {
		t.Fatalf("linha truncada deveria terminar em reticências: %q", long)
	}
}

func TestCosine(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0}, []float32{1, 0}, 1},
		{[]float32{1, 0}, []float32{0, 1}, 0},
		{[]float32{1, 0}, []float32{1, 1}, 0.7071067811865475},
		{nil, []float32{1, 0}, 0},
		{[]float32{0, 0}, []float32{1, 0}, 0},
		{[]float32{1, 0}, []float32{1, 0, 0}, 0},
	}
	for _, tc := range cases {
		if got := Cosine(tc.a, tc.b); math.Abs(got-tc.want) > 1e-9 {
			t.Fatalf("Cosine(%v, %v) = %v, esperava %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSummarizeAcumulaSemRepetirEmSequencia(t *testing.T) {
	dropped := []llm.Message{
		{Role: llm.RoleUser, Content: "primeira"},
		{Role: llm.RoleUser, Content: "primeira"},
		{Role: llm.RoleAssistant, Content: "resposta"},
		{Role: llm.RoleSystem, Content: "ignorada"},
	}
	got := Summarize(nil, dropped)
	want := []string{"- você: primeira", "- assistente: resposta"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Summarize = %v, esperava %v", got, want)
	}

	// Acumula sobre o resumo existente.
	got = Summarize(got, []llm.Message{{Role: llm.RoleUser, Content: "terceira"}})
	if len(got) != 3 || got[2] != "- você: terceira" {
		t.Fatalf("resumo acumulado = %v", got)
	}
}

func TestSummarizeRespeitaLimites(t *testing.T) {
	var dropped []llm.Message
	for i := 0; i < MaxNotes+5; i++ {
		dropped = append(dropped, llm.Message{Role: llm.RoleUser, Content: string(rune('a'+i%26)) + "-" + strings.Repeat("x", i%7+1)})
	}
	got := Summarize(nil, dropped)
	if len(got) != MaxNotes {
		t.Fatalf("resumo com %d linhas, esperava %d", len(got), MaxNotes)
	}

	// O limite de caracteres descarta as linhas mais antigas.
	var big []llm.Message
	for i := 0; i < MaxNotes; i++ {
		big = append(big, llm.Message{
			Role:    llm.RoleUser,
			Content: strings.Repeat("y", lineChars-1) + string(rune('a'+i%26)),
		})
	}
	got = Summarize(nil, big)
	if len(got) >= MaxNotes {
		t.Fatalf("limite de caracteres não descartou linhas antigas (%d)", len(got))
	}
	if total := len(strings.Join(got, "\n")); total > MaxChars {
		t.Fatalf("resumo com %d caracteres, esperava <= %d", total, MaxChars)
	}
	if !strings.HasSuffix(got[len(got)-1], string(rune('a'+(MaxNotes-1)%26))) {
		t.Fatalf("a linha mais recente deveria ser preservada: %q", got[len(got)-1])
	}
}

func TestNormalize(t *testing.T) {
	if _, ok := Normalize("   \n  "); ok {
		t.Fatal("nota vazia deveria ser rejeitada")
	}
	got, ok := Normalize("  usa\nGo  ")
	if !ok || got != "usa Go" {
		t.Fatalf("Normalize = %q, %v", got, ok)
	}
	got, _ = Normalize(strings.Repeat("z", MaxNoteChars+50))
	if n := len([]rune(got)); n > MaxNoteChars+4 {
		t.Fatalf("nota não truncada (%d runas)", n)
	}
}

func TestMergeSemDuplicar(t *testing.T) {
	known := []string{"a", "b"}
	got := Merge(known, []string{"b", "c", "", "a", "d"})
	want := []string{"a", "b", "c", "d"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Merge = %v, esperava %v", got, want)
	}
	if len(known) != 2 || known[0] != "a" {
		t.Fatalf("Merge não deveria alterar a entrada: %v", known)
	}

	var many []string
	for i := 0; i < MaxNotes+3; i++ {
		many = append(many, string(rune('A'+i))+"nota")
	}
	got = Merge(nil, many)
	if len(got) != MaxNotes || got[len(got)-1] != many[len(many)-1] {
		t.Fatalf("Merge deveria manter as %d notas mais recentes: %d", MaxNotes, len(got))
	}
}

func TestRenderEInject(t *testing.T) {
	if got := Render(nil, nil); len(got) != 0 {
		t.Fatalf("Render vazio = %+v", got)
	}
	msgs := Render([]string{"nota"}, []string{"- você: oi"})
	if len(msgs) != 2 {
		t.Fatalf("Render = %+v", msgs)
	}
	if !strings.HasPrefix(msgs[0].Content, NotesPrefix) || !strings.Contains(msgs[0].Content, "- nota") {
		t.Fatalf("bloco de notas = %q", msgs[0].Content)
	}
	if msgs[0].Role != llm.RoleSystem || msgs[1].Role != llm.RoleSystem {
		t.Fatalf("blocos deveriam ser system: %+v", msgs)
	}
	if !strings.HasPrefix(msgs[1].Content, Prefix) || !strings.Contains(msgs[1].Content, "- você: oi") {
		t.Fatalf("bloco de resumo = %q", msgs[1].Content)
	}

	history := []llm.Message{
		{Role: llm.RoleSystem, Content: "prompt"},
		{Role: llm.RoleSystem, Content: NotesPrefix + "antigo"},
		{Role: llm.RoleUser, Content: "pergunta"},
	}
	got := Inject(history, []string{"nota"}, nil)
	if len(got) != 3 {
		t.Fatalf("Inject = %+v", got)
	}
	if got[0].Content != "prompt" || !strings.HasPrefix(got[1].Content, NotesPrefix) || got[2].Content != "pergunta" {
		t.Fatalf("ordem inesperada: %+v", got)
	}

	// Sem memória, os blocos antigos saem e nada novo entra.
	if got := Inject(history, nil, nil); len(got) != 2 || got[1].Content != "pergunta" {
		t.Fatalf("Inject sem memória = %+v", got)
	}
	// Histórico sem system prompt: blocos entram no começo.
	got = Inject([]llm.Message{{Role: llm.RoleUser, Content: "oi"}}, []string{"nota"}, nil)
	if len(got) != 2 || got[1].Content != "oi" || !strings.HasPrefix(got[0].Content, NotesPrefix) {
		t.Fatalf("Inject sem system = %+v", got)
	}
}
