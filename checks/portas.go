package checks

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// CheckPortaTCP testa se uma porta TCP esta aberta no host.
func CheckPortaTCP(categoria, nome, host string, porta int, emit func(SubPasso)) Resultado {
	inicio := time.Now()
	r := Resultado{Categoria: categoria, Nome: nome}
	endereco := net.JoinHostPort(host, strconv.Itoa(porta))
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	conn, err := net.DialTimeout("tcp", endereco, 3*time.Second)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	r.Detalhes = map[string]interface{}{"host": host, "porta": porta}

	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Porta %d em %s nao respondeu: %s", porta, host, normalizaErroRede(err))
		add(SubPasso{
			Descricao: fmt.Sprintf("Conectar TCP em %s", endereco),
			Status:    StatusFail,
			DuracaoMs: r.DuracaoMs,
			Detalhe:   normalizaErroRede(err),
		})
		r.SubPassos = subpassos
		return r
	}
	conn.Close()
	r.Status = StatusOK
	r.Mensagem = fmt.Sprintf("Porta %d aberta em %s (%dms)", porta, host, r.DuracaoMs)
	add(SubPasso{
		Descricao: fmt.Sprintf("Conectar TCP em %s", endereco),
		Status:    StatusOK,
		DuracaoMs: r.DuracaoMs,
		Detalhe:   "Conexao estabelecida",
	})
	r.SubPassos = subpassos
	return r
}

// CheckVucaLocal testa se a URL do vucalocal responde.
// Aplica validacao estrita do status code (mesma logica do CheckHTTPS):
//   - 200-399 -> OK
//   - 404 com body "default backend" -> FAIL (URL aponta para um cluster
//     que nao tem rota pra essa instancia/host)
//   - 4xx qualquer -> WARN (resposta inesperada — pode estar errada)
//   - 5xx -> WARN (servidor com problema)
//   - Erro de rede (timeout/refused) -> FAIL
func CheckVucaLocal(url string, emit func(SubPasso)) Resultado {
	inicio := time.Now()
	r := Resultado{Categoria: "VucaLocal", Nome: "Endpoint VucaLocal"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	if url == "" {
		r.Status = StatusInfo
		r.Mensagem = "URL nao informada — pulando"
		add(SubPasso{
			Descricao: "Verificar URL informada",
			Status:    StatusInfo,
			Detalhe:   "Campo vazio — VucaLocal nao testado",
		})
		r.SubPassos = subpassos
		return r
	}
	add(SubPasso{
		Descricao: "Verificar URL informada",
		Status:    StatusOK,
		Detalhe:   url,
	})

	client := &http.Client{Timeout: 5 * time.Second}
	tReq := time.Now()
	resp, err := client.Get(url)
	durReq := time.Since(tReq).Milliseconds()
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	r.Detalhes = map[string]interface{}{"url": url}
	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Falha ao acessar %s: %s", url, normalizaErroRede(err))
		add(SubPasso{
			Descricao: "Requisitar GET na URL",
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   normalizaErroRede(err),
		})
		r.SubPassos = subpassos
		return r
	}
	defer resp.Body.Close()
	add(SubPasso{
		Descricao: "Requisitar GET na URL",
		Status:    StatusOK,
		DuracaoMs: durReq,
		Detalhe:   fmt.Sprintf("Resposta HTTP %d recebida", resp.StatusCode),
	})

	tBody := time.Now()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	durBody := time.Since(tBody).Milliseconds()
	body := string(bodyBytes)
	snippet := strings.TrimSpace(body)
	if len(snippet) > 200 {
		snippet = snippet[:200] + "..."
	}
	add(SubPasso{
		Descricao: "Ler corpo da resposta (ate 2KB)",
		Status:    StatusOK,
		DuracaoMs: durBody,
		Detalhe:   fmt.Sprintf("%d bytes lidos", len(bodyBytes)),
	})

	r.Detalhes["status_code"] = resp.StatusCode
	r.Detalhes["body_snippet"] = snippet

	var statusClassif Status
	var detClassif string
	switch {
	case resp.StatusCode == 404 && strings.Contains(strings.ToLower(body), "default backend"):
		r.Status = StatusFail
		r.Mensagem = "VucaLocal aponta para uma URL inexistente — o cluster respondeu \"default backend - 404\". Verifique se a URL esta correta."
		statusClassif = StatusFail
		detClassif = "404 com body 'default backend' — URL invalida"
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Aviso: VucaLocal respondeu %d (resposta inesperada — confira se a URL esta correta)", resp.StatusCode)
		statusClassif = StatusWarn
		detClassif = fmt.Sprintf("4xx inesperado (%d)", resp.StatusCode)
	case resp.StatusCode >= 500:
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Aviso: VucaLocal respondeu %d (erro do servidor)", resp.StatusCode)
		statusClassif = StatusWarn
		detClassif = fmt.Sprintf("5xx — erro do servidor (%d)", resp.StatusCode)
	default:
		r.Status = StatusOK
		r.Mensagem = fmt.Sprintf("VucaLocal respondeu %d em %dms", resp.StatusCode, r.DuracaoMs)
		statusClassif = StatusOK
		detClassif = fmt.Sprintf("Status %d — resposta valida", resp.StatusCode)
	}
	add(SubPasso{
		Descricao: "Classificar status code",
		Status:    statusClassif,
		Detalhe:   detClassif,
	})
	r.SubPassos = subpassos
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
