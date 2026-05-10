package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/vucasolution/vuca-infra-diagnostico/checks"
)

// AssetsFS recebe o embed.FS do main com a pasta web/.
type Server struct {
	Assets fs.FS
}

func New(assets fs.FS) *Server {
	return &Server{Assets: assets}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(s.Assets))))
	mux.HandleFunc("/api/diagnosticar", s.handleDiagnosticar)
	mux.HandleFunc("/api/relatorio.html", s.handleRelatorioHTML)
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.Assets, "index.html")
	if err != nil {
		http.Error(w, "index nao encontrado", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) handleDiagnosticar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "metodo invalido", http.StatusMethodNotAllowed)
		return
	}
	var cfg checks.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		http.Error(w, "json invalido: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, _ := w.(http.Flusher)
	progresso := make(chan checks.Resultado, 8)
	enc := json.NewEncoder(w)

	go func() {
		for res := range progresso {
			enc.Encode(map[string]interface{}{"tipo": "resultado", "dados": res})
			if flusher != nil {
				flusher.Flush()
			}
		}
	}()

	rel := checks.Executar(cfg, progresso)
	enc.Encode(map[string]interface{}{"tipo": "final", "dados": rel})
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) handleRelatorioHTML(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "metodo invalido", http.StatusMethodNotAllowed)
		return
	}
	var rel checks.Relatorio
	if err := json.NewDecoder(r.Body).Decode(&rel); err != nil {
		http.Error(w, "json invalido", http.StatusBadRequest)
		return
	}
	tplBytes, err := fs.ReadFile(s.Assets, "relatorio.html")
	if err != nil {
		http.Error(w, "template nao encontrado", http.StatusInternalServerError)
		return
	}
	tpl, err := template.New("rel").Funcs(template.FuncMap{
		"json": func(v interface{}) string {
			b, _ := json.MarshalIndent(v, "", "  ")
			return string(b)
		},
		"statusClass": func(s checks.Status) string {
			switch s {
			case checks.StatusOK:
				return "ok"
			case checks.StatusWarn:
				return "warn"
			case checks.StatusFail:
				return "fail"
			default:
				return "info"
			}
		},
		"contar": func(rs []checks.Resultado, st checks.Status) int {
			n := 0
			for _, r := range rs {
				if r.Status == st {
					n++
				}
			}
			return n
		},
		"formatar": func(t time.Time) string {
			return t.Format("02/01/2006 15:04:05")
		},
	}).Parse(string(tplBytes))
	if err != nil {
		http.Error(w, "erro no template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	stamp := time.Now().Format("2006-01-02-1504")
	filename := fmt.Sprintf("relatorio-%s-%s.html", strings.ToLower(rel.Config.Instancia), stamp)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	tpl.Execute(w, rel)
}

// EmbedSub retorna a sub-fs apontando pra pasta `web/`.
func EmbedSub(efs embed.FS) (fs.FS, error) {
	return fs.Sub(efs, "web")
}
