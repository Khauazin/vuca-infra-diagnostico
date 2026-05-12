package main

import (
	"embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/vucasolution/vuca-infra-diagnostico/server"
)

//go:embed web
var webFS embed.FS

const (
	porta          = 7777
	nomeArquivoLog = "vuca-diag.log"
)

func main() {
	// (1) Configura logger: escreve em stdout E em vuca-diag.log (ao lado do .exe)
	caminhoLog := configurarLogger()
	log.Printf("=== Vuca Infra Diagnostico iniciando ===")
	log.Printf("Log sendo gravado em: %s", caminhoLog)

	// (2) Recover global para qualquer panic nao capturado nao derrubar o app
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[PANIC] panic na main: %v", r)
		}
	}()

	assets, err := server.EmbedSub(webFS)
	if err != nil {
		log.Fatalf("[FATAL] falha ao montar assets: %v", err)
	}

	srv := server.New(assets)
	addr := fmt.Sprintf("127.0.0.1:%d", porta)
	url := fmt.Sprintf("http://%s/", addr)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("[FATAL] falha ao escutar em %s: %v", addr, err)
	}

	fmt.Println("==========================================")
	fmt.Println(" Vuca Infra Diagnostico")
	fmt.Printf(" Servidor rodando em %s\n", url)
	fmt.Printf(" Log: %s\n", caminhoLog)
	fmt.Println(" Feche esta janela para encerrar.")
	fmt.Println("==========================================")
	log.Printf("[INFO] Servidor rodando em %s", url)

	go func() {
		time.Sleep(500 * time.Millisecond)
		abrirNavegador(url)
	}()

	if err := http.Serve(ln, srv.Routes()); err != nil {
		log.Fatalf("[FATAL] servidor encerrado com erro: %v", err)
	}
}

// configurarLogger configura o log padrao do pacote para escrever em stdout
// E num arquivo `vuca-diag.log` ao lado do executavel. Retorna o caminho do
// arquivo de log para exibicao na inicializacao. Se nao conseguir criar o
// arquivo (permissao etc), continua so com stdout sem falhar.
func configurarLogger() string {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	exe, err := os.Executable()
	if err != nil {
		// Sem acesso ao caminho do executavel, mantem so stdout
		return "(apenas stdout — falha ao obter caminho do executavel)"
	}
	dir := filepath.Dir(exe)
	caminho := filepath.Join(dir, nomeArquivoLog)

	arq, err := os.OpenFile(caminho, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		// Sem permissao para escrever, mantem so stdout
		return fmt.Sprintf("(apenas stdout — falha ao abrir %s: %v)", caminho, err)
	}

	// Escreve em ambos: stdout (terminal) + arquivo (persistencia)
	log.SetOutput(io.MultiWriter(os.Stdout, arq))
	return caminho
}

func abrirNavegador(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	exec.Command(cmd, args...).Start()
}
