// Command specbench mede tokens/s e aceitação do draft do llama-server para
// várias configurações de decodificação especulativa, em sequência.
//
// Exemplo (CPU, guloso, matriz enxuta):
//
//	specbench -model ~/llama.cpp/models/qwen2.5-7b-instruct-q4_k_m.gguf \
//	  -draft ~/llama.cpp/models/qwen2.5-0.5b-instruct-q4_k_m.gguf \
//	  -t 10 -nmax 0,3,4,8 -td 0,8 -n 128
//
// Importante: use -temp 0 (guloso). Com temperatura alta a aceitação do draft
// despenca e a especulativa fica mais lenta que a geração normal.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"text/tabwriter"

	"agentic-harness/internal/specbench"
)

func main() {
	home, _ := os.UserHomeDir()
	defModels := filepath.Join(home, "llama.cpp", "models")

	server := flag.String("server", filepath.Join(home, "llama.cpp", "build", "bin", "llama-server"), "binário llama-server")
	target := flag.String("model", filepath.Join(defModels, "qwen2.5-7b-instruct-q4_k_m.gguf"), "modelo alvo (GGUF)")
	draft := flag.String("draft", filepath.Join(defModels, "qwen2.5-0.5b-instruct-q4_k_m.gguf"), "modelo draft (vazio = sem especulativa)")
	specType := flag.String("spectype", "draft-simple", "tipo de especulativa (--spec-type)")
	prompt := flag.String("prompt", "Escreva um paragrafo explicando o que e computacao quantica, em portugues.", "prompt fixo")
	npred := flag.Int("n", 128, "tokens a gerar por caso")
	threads := flag.Int("t", 10, "threads de geração (-t)")
	threadsBatch := flag.Int("tb", 0, "threads de batch (-tb; 0 = padrão)")
	nmaxList := flag.String("nmax", "0,3,4,8", "lista de --spec-draft-n-max (0 = baseline sem especulativa)")
	tdList := flag.String("td", "0", "lista de threads do draft (-td; 0 = padrão)")
	ngl := flag.Int("ngl", 0, "camadas do alvo na GPU (-ngl)")
	ngld := flag.Int("ngld", 0, "camadas do draft na GPU (-ngld)")
	ctxSize := flag.Int("c", 2048, "tamanho do contexto (-c)")
	basePort := flag.Int("port", 8123, "porta base do llama-server")
	temp := flag.Float64("temp", 0, "temperatura (0 = guloso; essencial para a especulativa render)")
	memMB := flag.Int("memlimit", 0, "limite de memória do processo Go em MB (0 = desliga)")
	gcpct := flag.Int("gc", -1, "GOGC percent (-1 = padrão)")
	logdir := flag.String("logdir", os.TempDir(), "diretório para os logs do servidor")
	jsonOut := flag.Bool("json", false, "imprime os resultados em JSON")
	flag.Parse()

	if *gcpct >= 0 {
		debug.SetGCPercent(*gcpct)
	}
	if *memMB > 0 {
		debug.SetMemoryLimit(int64(*memMB) << 20)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	nmaxes := parseIntList(*nmaxList)
	tds := parseIntList(*tdList)

	var results []specbench.Result
	port := *basePort
	baseTPS := 0.0

	for _, nmax := range nmaxes {
		for _, td := range tds {
			if nmax == 0 && td != tds[0] {
				continue // o baseline não depende de -td
			}
			name := fmt.Sprintf("nmax%d_td%d", nmax, td)
			c := specbench.Case{
				Name:         name,
				Target:       *target,
				Threads:      *threads,
				ThreadsBatch: *threadsBatch,
				CtxSize:      *ctxSize,
				NGL:          *ngl,
				Prompt:       *prompt,
				NPredict:     *npred,
				Temperature:  *temp,
				Port:         port,
				ServerBin:    *server,
				LogPath:      filepath.Join(*logdir, "specbench_"+name+".log"),
			}
			if nmax > 0 {
				c.Draft = *draft
				c.SpecType = *specType
				c.ThreadsDraft = td
				c.NGLD = *ngld
				c.NMax = nmax
			} else {
				c.SpecType = "none"
			}

			fmt.Fprintf(os.Stderr, ">> %s (porta %d)...\n", name, port)
			res := specbench.Run(ctx, c)
			results = append(results, res)
			if nmax == 0 && res.Err == nil {
				baseTPS = res.GenTPS
			}
			port++
			runtime.GC()
			if ctx.Err() != nil {
				break
			}
		}
		if ctx.Err() != nil {
			break
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(results)
		return
	}
	printTable(results, baseTPS)
}

func printTable(results []specbench.Result, baseTPS float64) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "caso\tger (t/s)\tacel.\taceit.\tmean len\tstatus")
	for _, r := range results {
		if r.Err != nil {
			fmt.Fprintf(w, "%s\t-\t-\t-\t-\tERRO: %v\n", r.Case.Name, r.Err)
			continue
		}
		speed := "-"
		if baseTPS > 0 {
			speed = fmt.Sprintf("%.2fx", r.GenTPS/baseTPS)
		}
		acc, mean := "-", "-"
		if r.HasAccept {
			acc = fmt.Sprintf("%.1f%%", r.Accept.Rate*100)
			mean = fmt.Sprintf("%.2f", r.Accept.MeanLen)
		}
		fmt.Fprintf(w, "%s\t%.2f\t%s\t%s\t%s\tok\n", r.Case.Name, r.GenTPS, speed, acc, mean)
	}
	w.Flush()
}

func parseIntList(s string) []int {
	parts := strings.Split(s, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		out = []int{0}
	}
	return out
}
