package checks

import (
	"fmt"
	"net"
	"strconv"
	"time"
)

// CheckImpressora testa conectividade TCP com a impressora.
func CheckImpressora(imp Impressora, emit func(SubPasso)) Resultado {
	inicio := time.Now()
	nome := imp.Nome
	if nome == "" {
		nome = imp.IP
	}
	r := Resultado{Categoria: "Impressoras", Nome: nome}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	porta := imp.Porta
	if porta == 0 {
		porta = 9100
	}

	endereco := net.JoinHostPort(imp.IP, strconv.Itoa(porta))
	conn, err := net.DialTimeout("tcp", endereco, 3*time.Second)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	r.Detalhes = map[string]interface{}{
		"nome":  nome,
		"ip":    imp.IP,
		"porta": porta,
	}
	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("%s (%s:%d) nao respondeu: %s", nome, imp.IP, porta, normalizaErroRede(err))
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
	r.Mensagem = fmt.Sprintf("%s (%s:%d) online — %dms", nome, imp.IP, porta, r.DuracaoMs)
	add(SubPasso{
		Descricao: fmt.Sprintf("Conectar TCP em %s", endereco),
		Status:    StatusOK,
		DuracaoMs: r.DuracaoMs,
		Detalhe:   "Conexao estabelecida",
	})
	r.SubPassos = subpassos
	return r
}
