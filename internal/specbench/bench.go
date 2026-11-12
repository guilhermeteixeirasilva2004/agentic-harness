// Package specbench mede o desempenho do llama-server — inclusive a decodificação
// especulativa — gerenciando o ciclo de vida do processo e lendo as estatísticas
// de aceitação do draft que o servidor escreve no log.
package specbench

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Case descreve uma configuração do llama-server a ser medida.
type Case struct {
	Name         string
	Target       string // modelo alvo (GGUF)
	Draft        string // modelo draft (vazio = sem especulativa)
	SpecType     string // "draft-simple", "ngram-simple", ... (vazio = padrão)
	Threads      int    // -t
	ThreadsBatch int    // -tb (0 = padrão)
	ThreadsDraft int    // -td (0 = padrão)
	NMax         int    // --spec-draft-n-max (0 = padrão)
	CtxSize      int    // -c
	NGL          int    // -ngl
	NGLD         int    // -ngld
	Prompt       string
	NPredict     int
	Temperature  float64
	Port         int
	ServerBin    string        // binário llama-server
	LogPath      string        // onde a saída do servidor é gravada
	Timeout      time.Duration // limite total (subida + geração)
}

// Acceptance resume a linha "draft acceptance = ..." do llama-server.
type Acceptance struct {
	Rate      float64
	Accepted  int
	Generated int
	MeanLen   float64
}

// Result é o resultado de um caso medido.
type Result struct {
	Case       Case
	PromptTPS  float64
	GenTPS     float64
	PredictedN int
	Accept     Acceptance
	HasAccept  bool
	Elapsed    time.Duration
	Err        error
}

type timings struct {
	PromptN         int     `json:"prompt_n"`
	PromptPerSecond float64 `json:"prompt_per_second"`
	PredictedN      int     `json:"predicted_n"`
	PredictedPerSec float64 `json:"predicted_per_second"`
}

type completionRequest struct {
	Prompt      string  `json:"prompt"`
	NPredict    int     `json:"n_predict"`
	Temperature float64 `json:"temperature"`
	TopK        int     `json:"top_k"`
	TopP        float64 `json:"top_p"`
	Stream      bool    `json:"stream"`
	CachePrompt bool    `json:"cache_prompt"`
}

type completionResponse struct {
	Timings timings `json:"timings"`
}

var acceptanceRE = regexp.MustCompile(`draft acceptance\s*=\s*([0-9.]+)\s*\(\s*([0-9]+)\s*accepted\s*/\s*([0-9]+)\s*generated\s*\),\s*mean len\s*=\s*([0-9.]+)`)

// ParseAcceptance extrai as estatísticas da linha "draft acceptance = ...".
func ParseAcceptance(line string) (Acceptance, bool) {
	m := acceptanceRE.FindStringSubmatch(line)
	if m == nil {
		return Acceptance{}, false
	}
	rate, e1 := strconv.ParseFloat(m[1], 64)
	acc, e2 := strconv.Atoi(m[2])
	gen, e3 := strconv.Atoi(m[3])
	mean, e4 := strconv.ParseFloat(m[4], 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return Acceptance{}, false
	}
	return Acceptance{Rate: rate, Accepted: acc, Generated: gen, MeanLen: mean}, true
}

// withDefaults completa os campos que ficaram zerados.
func (c Case) withDefaults() Case {
	if c.ServerBin == "" {
		c.ServerBin = "llama-server"
	}
	if c.Draft != "" && c.SpecType == "" {
		c.SpecType = "draft-simple"
	}
	if c.Threads == 0 {
		c.Threads = 10
	}
	if c.CtxSize == 0 {
		c.CtxSize = 2048
	}
	if c.NPredict == 0 {
		c.NPredict = 128
	}
	if c.Port == 0 {
		c.Port = 8123
	}
	if c.Timeout == 0 {
		c.Timeout = 15 * time.Minute
	}
	if c.LogPath == "" {
		c.LogPath = filepath.Join(os.TempDir(), "specbench.log")
	}
	return c
}

// args monta a linha de comando do llama-server para o caso.
func (c Case) args() []string {
	a := []string{
		"-m", c.Target,
		"-c", strconv.Itoa(c.CtxSize),
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(c.Port),
		"-ngl", strconv.Itoa(c.NGL),
		"-t", strconv.Itoa(c.Threads),
		"-np", "1",
		"-b", "2048",
	}
	if c.ThreadsBatch > 0 {
		a = append(a, "-tb", strconv.Itoa(c.ThreadsBatch))
	}
	if c.SpecType != "" && c.SpecType != "none" {
		a = append(a, "--spec-type", c.SpecType)
		if c.Draft != "" {
			a = append(a, "-md", c.Draft)
		}
		if c.ThreadsDraft > 0 {
			a = append(a, "-td", strconv.Itoa(c.ThreadsDraft))
		}
		if c.NGLD != 0 {
			a = append(a, "-ngld", strconv.Itoa(c.NGLD))
		}
		if c.NMax > 0 {
			a = append(a, "--spec-draft-n-max", strconv.Itoa(c.NMax))
		}
	}
	return a
}

// Run sobe o servidor, espera ficar saudável, mede uma geração e encerra o processo.
func Run(ctx context.Context, c Case) (res Result) {
	c = c.withDefaults()
	res = Result{Case: c}
	start := time.Now()
	defer func() { res.Elapsed = time.Since(start) }()

	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	if err := os.MkdirAll(filepath.Dir(c.LogPath), 0o755); err != nil {
		res.Err = fmt.Errorf("preparar diretório de log: %w", err)
		return
	}
	logf, err := os.Create(c.LogPath)
	if err != nil {
		res.Err = fmt.Errorf("criar log: %w", err)
		return
	}

	cmd := exec.Command(c.ServerBin, c.args()...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	if err := cmd.Start(); err != nil {
		logf.Close()
		res.Err = fmt.Errorf("iniciar servidor: %w", err)
		return
	}

	if err := waitHealthy(ctx, c.Port); err != nil {
		stop(cmd, logf)
		res.Err = err
		return
	}

	tr, reqErr := complete(ctx, c)
	stop(cmd, logf)

	if acc, ok := readLastAcceptance(c.LogPath); ok {
		res.Accept, res.HasAccept = acc, true
	}
	if reqErr != nil {
		res.Err = reqErr
		return
	}
	res.PromptTPS = tr.PromptPerSecond
	res.GenTPS = tr.PredictedPerSec
	res.PredictedN = tr.PredictedN
	return
}

// stop encerra o servidor (SIGTERM e, se preciso, SIGKILL) e fecha o log.
func stop(cmd *exec.Cmd, logf *os.File) {
	if cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	}
	_ = logf.Close()
}

// waitHealthy faz polling em /health até o servidor responder 200.
func waitHealthy(ctx context.Context, port int) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	client := &http.Client{Timeout: time.Second}
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("montar checagem de saúde: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			code := resp.StatusCode
			resp.Body.Close()
			if code == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("servidor não ficou saudável na porta %d: %w", port, ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// complete dispara uma geração gulosa em /completion e devolve os tempos.
func complete(ctx context.Context, c Case) (timings, error) {
	body, err := json.Marshal(completionRequest{
		Prompt:      c.Prompt,
		NPredict:    c.NPredict,
		Temperature: c.Temperature,
		TopK:        40,
		TopP:        0.95,
		Stream:      false,
		CachePrompt: false,
	})
	if err != nil {
		return timings{}, fmt.Errorf("codificar requisição: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/completion", c.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return timings{}, fmt.Errorf("montar requisição: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{} // o prazo vem do contexto
	resp, err := client.Do(req)
	if err != nil {
		return timings{}, fmt.Errorf("chamar /completion: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return timings{}, fmt.Errorf("servidor respondeu %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var cr completionResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return timings{}, fmt.Errorf("decodificar resposta: %w", err)
	}
	return cr.Timings, nil
}

// readLastAcceptance devolve a última linha "draft acceptance" do log do servidor.
func readLastAcceptance(path string) (Acceptance, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Acceptance{}, false
	}
	defer f.Close()

	var last Acceptance
	var found bool
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if a, ok := ParseAcceptance(sc.Text()); ok {
			last, found = a, true
		}
	}
	return last, found
}
