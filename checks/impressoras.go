package checks

import (
	"fmt"
	"net"
	"time"
)

// CheckImpressora testa conectividade TCP com a impressora.
func CheckImpressora(imp Impressora) Resultado {
	inicio := time.Now()
	nome := imp.Nome
	if nome == "" {
		nome = imp.IP
	}
	r := Resultado{Categoria: "Impressoras", Nome: nome}

	porta := imp.Porta
	if porta == 0 {
		porta = 9100
	}

	endereco := fmt.Sprintf("%s:%d", imp.IP, porta)
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
		return r
	}
	conn.Close()
	r.Status = StatusOK
	r.Mensagem = fmt.Sprintf("%s (%s:%d) online — %dms", nome, imp.IP, porta, r.DuracaoMs)
	return r
}
