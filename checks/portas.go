package checks

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// CheckPortaTCP testa se uma porta TCP esta aberta no host.
func CheckPortaTCP(categoria, nome, host string, porta int) Resultado {
	inicio := time.Now()
	r := Resultado{Categoria: categoria, Nome: nome}
	endereco := fmt.Sprintf("%s:%d", host, porta)

	conn, err := net.DialTimeout("tcp", endereco, 3*time.Second)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	r.Detalhes = map[string]interface{}{"host": host, "porta": porta}

	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Porta %d em %s nao respondeu: %s", porta, host, normalizaErroRede(err))
		return r
	}
	conn.Close()
	r.Status = StatusOK
	r.Mensagem = fmt.Sprintf("Porta %d aberta em %s (%dms)", porta, host, r.DuracaoMs)
	return r
}

// CheckVucaLocal testa se a URL do vucalocal responde.
func CheckVucaLocal(url string) Resultado {
	inicio := time.Now()
	r := Resultado{Categoria: "VucaLocal", Nome: "Endpoint VucaLocal"}
	if url == "" {
		r.Status = StatusInfo
		r.Mensagem = "URL nao informada — pulando"
		return r
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	r.Detalhes = map[string]interface{}{"url": url}
	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Falha ao acessar %s: %s", url, normalizaErroRede(err))
		return r
	}
	defer resp.Body.Close()
	r.Detalhes["status_code"] = resp.StatusCode
	r.Status = StatusOK
	r.Mensagem = fmt.Sprintf("VucaLocal respondeu %d em %dms", resp.StatusCode, r.DuracaoMs)
	return r
}

func normalizaErroRede(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "connection refused") {
		return "conexao recusada (servico parado ou porta fechada)"
	}
	if strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded") {
		return "timeout (host inacessivel ou firewall bloqueando)"
	}
	if strings.Contains(msg, "no such host") {
		return "host nao encontrado (DNS falhou)"
	}
	return msg
}
