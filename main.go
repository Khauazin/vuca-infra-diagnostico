package main

import (
	"embed"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/vucasolution/vuca-infra-diagnostico/server"
)

//go:embed web
var webFS embed.FS

const (
	porta = 7777
)

func main() {
	assets, err := server.EmbedSub(webFS)
	if err != nil {
		log.Fatalf("falha ao montar assets: %v", err)
	}

	srv := server.New(assets)
	addr := fmt.Sprintf("127.0.0.1:%d", porta)
	url := fmt.Sprintf("http://%s/", addr)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("falha ao escutar em %s: %v", addr, err)
	}

	fmt.Println("==========================================")
	fmt.Println(" Vuca Infra Diagnostico")
	fmt.Printf(" Servidor rodando em %s\n", url)
	fmt.Println(" Feche esta janela para encerrar.")
	fmt.Println("==========================================")

	go func() {
		time.Sleep(500 * time.Millisecond)
		abrirNavegador(url)
	}()

	if err := http.Serve(ln, srv.Routes()); err != nil {
		log.Fatal(err)
	}
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
