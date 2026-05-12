package checks

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// CheckRabbitMQAMQP valida que a porta 5672 do RabbitMQ esta realmente
// expondo o protocolo AMQP (e nao qualquer outro servico TCP rodando alocado
// naquela porta). Faz o handshake inicial AMQP 0-9-1: envia o header
// "AMQP\x00\x00\x09\x01" e espera resposta do servidor (frame Connection.Start).
//
// Detecta:
//   - Porta aberta mas servico errado (porta hijacked) → FAIL
//   - Versao AMQP incompativel → WARN
//   - Servidor AMQP saudavel → OK
func CheckRabbitMQAMQP(host string, porta int, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "RabbitMQ", Nome: fmt.Sprintf("Protocolo AMQP em %s:%d", host, porta)}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	inicio := time.Now()
	endereco := net.JoinHostPort(host, strconv.Itoa(porta))

	tConn := time.Now()
	conn, err := net.DialTimeout("tcp", endereco, 5*time.Second)
	durConn := time.Since(tConn).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: fmt.Sprintf("Conectar TCP em %s", endereco),
			Status:    StatusFail,
			DuracaoMs: durConn,
			Detalhe:   normalizaErroRede(err),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Falha de conexao TCP em %s: %s", endereco, normalizaErroRede(err))
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer conn.Close()
	add(SubPasso{
		Descricao: fmt.Sprintf("Conectar TCP em %s", endereco),
		Status:    StatusOK,
		DuracaoMs: durConn,
		Detalhe:   "Conexao TCP estabelecida",
	})

	// AMQP 0-9-1 protocol header: "AMQP" + 0x00 + 0x00 + 0x09 + 0x01
	header := []byte{'A', 'M', 'Q', 'P', 0x00, 0x00, 0x09, 0x01}
	conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	tSend := time.Now()
	_, errSend := conn.Write(header)
	durSend := time.Since(tSend).Milliseconds()
	if errSend != nil {
		add(SubPasso{
			Descricao: "Enviar header AMQP 0-9-1",
			Status:    StatusFail,
			DuracaoMs: durSend,
			Detalhe:   errSend.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Conexao caiu ao enviar header AMQP — servidor parece nao ser AMQP"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: "Enviar header AMQP 0-9-1",
		Status:    StatusOK,
		DuracaoMs: durSend,
		Detalhe:   "8 bytes do protocol header enviados",
	})

	// Le resposta. AMQP frame: type(1) + channel(2) + size(4) + payload + 0xCE.
	// Em caso de versao incompativel, servidor responde com 8 bytes
	// "AMQP\x00\x00\xXX\xYY" indicando versao suportada.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	tRead := time.Now()
	resposta := make([]byte, 32)
	n, errRead := io.ReadAtLeast(conn, resposta, 7)
	durRead := time.Since(tRead).Milliseconds()
	if errRead != nil {
		add(SubPasso{
			Descricao: "Aguardar resposta do servidor (Connection.Start)",
			Status:    StatusFail,
			DuracaoMs: durRead,
			Detalhe:   fmt.Sprintf("Sem resposta valida (%d bytes recebidos): %s — servidor nao parece ser AMQP", n, errRead.Error()),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Servidor TCP esta na porta mas nao responde ao handshake AMQP — provavelmente servico errado ocupando a porta"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	// Caso 1: servidor devolveu protocol header (incompatibilidade de versao)
	if n >= 8 && string(resposta[:4]) == "AMQP" {
		versaoSugerida := fmt.Sprintf("%d.%d.%d", resposta[5], resposta[6], resposta[7])
		add(SubPasso{
			Descricao: "Aguardar resposta do servidor (Connection.Start)",
			Status:    StatusWarn,
			DuracaoMs: durRead,
			Detalhe:   fmt.Sprintf("Servidor sugeriu outra versao AMQP: %s — pode causar incompatibilidade", versaoSugerida),
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("AMQP responde mas em versao incompativel (%s)", versaoSugerida)
		r.Detalhes = map[string]interface{}{"versao_sugerida": versaoSugerida, "bytes_recebidos": n}
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	// Caso 2: frame normal — tipo=1 (method), classe=10 (Connection), metodo=10 (Start)
	tipoFrame := resposta[0]
	if tipoFrame != 1 {
		add(SubPasso{
			Descricao: "Aguardar resposta do servidor (Connection.Start)",
			Status:    StatusFail,
			DuracaoMs: durRead,
			Detalhe:   fmt.Sprintf("Frame com tipo %d (esperado 1 = method) — servico responde mas nao parece ser AMQP valido", tipoFrame),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Servico nao parece ser AMQP (frame tipo %d em vez de 1)", tipoFrame)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	// Le classe/metodo (uint16 cada, 4 bytes depois do header de 7 bytes)
	classe := binary.BigEndian.Uint16(resposta[7:9])
	if n < 11 {
		add(SubPasso{
			Descricao: "Aguardar resposta do servidor (Connection.Start)",
			Status:    StatusWarn,
			DuracaoMs: durRead,
			Detalhe:   fmt.Sprintf("Frame muito curto (%d bytes) — pode estar truncado", n),
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = "Resposta AMQP truncada"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	metodo := binary.BigEndian.Uint16(resposta[9:11])
	if classe == 10 && metodo == 10 {
		add(SubPasso{
			Descricao: "Aguardar resposta do servidor (Connection.Start)",
			Status:    StatusOK,
			DuracaoMs: durRead,
			Detalhe:   fmt.Sprintf("Recebeu Connection.Start (classe=%d, metodo=%d, %d bytes) — servidor AMQP saudavel", classe, metodo, n),
		})
		r.SubPassos = subpassos
		r.Status = StatusOK
		r.Mensagem = "AMQP responde normalmente — servidor RabbitMQ saudavel"
		r.Detalhes = map[string]interface{}{
			"classe":         classe,
			"metodo":         metodo,
			"bytes_recebidos": n,
		}
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: "Aguardar resposta do servidor (Connection.Start)",
		Status:    StatusWarn,
		DuracaoMs: durRead,
		Detalhe:   fmt.Sprintf("Frame AMQP recebido mas classe/metodo inesperado (%d/%d, esperado 10/10)", classe, metodo),
	})
	r.SubPassos = subpassos
	r.Status = StatusWarn
	r.Mensagem = fmt.Sprintf("AMQP responde mas com frame inesperado (classe=%d, metodo=%d)", classe, metodo)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckImpressoraESC valida que uma impressora respondendo em TCP/9100
// realmente fala ESC/POS. Envia o comando "real time status request" (DLE
// EOT 1 = 0x10 0x04 0x01) que retorna 1 byte com status. Impressoras genericas
// ou servicos errados ocupando a porta nao respondem a esse comando.
//
// Nao imprime nada — o comando e' so' uma consulta de status.
//
// Status:
//   - OK: TCP conecta e impressora responde 1 byte de status (ESC/POS valido)
//   - WARN: TCP conecta mas impressora nao responde a ESC/POS (pode ser modelo
//     que nao suporta status real-time, ou impressora travada)
//   - FAIL: TCP nao conecta
func CheckImpressoraESC(imp Impressora, emit func(SubPasso)) Resultado {
	nome := imp.Nome
	if nome == "" {
		nome = imp.IP
	}
	r := Resultado{Categoria: "Impressoras", Nome: nome + " (ESC/POS)"}
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
	inicio := time.Now()

	tConn := time.Now()
	conn, err := net.DialTimeout("tcp", endereco, 3*time.Second)
	durConn := time.Since(tConn).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: fmt.Sprintf("Conectar TCP em %s", endereco),
			Status:    StatusFail,
			DuracaoMs: durConn,
			Detalhe:   normalizaErroRede(err),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Impressora %s nao respondeu: %s", endereco, normalizaErroRede(err))
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer conn.Close()
	add(SubPasso{
		Descricao: fmt.Sprintf("Conectar TCP em %s", endereco),
		Status:    StatusOK,
		DuracaoMs: durConn,
		Detalhe:   "Conexao estabelecida",
	})

	// Envia status request real-time (DLE EOT 1). Nao imprime nada.
	cmd := []byte{0x10, 0x04, 0x01}
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	tSend := time.Now()
	_, errSend := conn.Write(cmd)
	durSend := time.Since(tSend).Milliseconds()
	if errSend != nil {
		add(SubPasso{
			Descricao: "Enviar status request ESC/POS (DLE EOT 1)",
			Status:    StatusWarn,
			DuracaoMs: durSend,
			Detalhe:   errSend.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Impressora %s conecta mas recusou comando ESC/POS — pode estar travada", endereco)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: "Enviar status request ESC/POS (DLE EOT 1)",
		Status:    StatusOK,
		DuracaoMs: durSend,
		Detalhe:   "3 bytes enviados (comando nao imprime nada)",
	})

	// Le resposta de status. Esperado 1 byte de status.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	tRead := time.Now()
	resposta := make([]byte, 4)
	n, errRead := conn.Read(resposta)
	durRead := time.Since(tRead).Milliseconds()
	if errRead != nil || n == 0 {
		add(SubPasso{
			Descricao: "Aguardar status da impressora",
			Status:    StatusWarn,
			DuracaoMs: durRead,
			Detalhe:   "Sem resposta ao status request — pode ser impressora generica sem suporte a real-time status, ou modelo USB-emulado",
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Impressora %s aceita TCP mas nao responde a comando ESC/POS (pode ser modelo limitado ou travada)", endereco)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	statusByte := resposta[0]
	descricaoStatus := descreverStatusESCPOS(statusByte)
	add(SubPasso{
		Descricao: "Aguardar status da impressora",
		Status:    StatusOK,
		DuracaoMs: durRead,
		Detalhe:   fmt.Sprintf("Status byte: 0x%02X — %s", statusByte, descricaoStatus),
	})

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"endereco":   endereco,
		"status_byte": fmt.Sprintf("0x%02X", statusByte),
		"status":     descricaoStatus,
	}
	r.Status = StatusOK
	r.Mensagem = fmt.Sprintf("Impressora %s respondeu ESC/POS — %s", endereco, descricaoStatus)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// descreverStatusESCPOS converte o byte de status ESC/POS em descricao humana.
// Bit layout (ESC/POS spec, comando "DLE EOT 1"):
//
//	bit 0,1: fixos
//	bit 2: drawer kick-out pin
//	bit 3: 0=online, 1=offline
//	bit 4: fixo
//	bit 5: 0=cover ok, 1=cover open
//	bit 6: 0=paper ok, 1=paper near end (ou sem papel)
//	bit 7: fixo
func descreverStatusESCPOS(b byte) string {
	avisos := []string{}
	if b&0x08 != 0 {
		avisos = append(avisos, "offline")
	}
	if b&0x20 != 0 {
		avisos = append(avisos, "tampa aberta")
	}
	if b&0x40 != 0 {
		avisos = append(avisos, "papel acabando")
	}
	if len(avisos) == 0 {
		return "impressora online e pronta"
	}
	return "impressora reportou: " + strings.Join(avisos, ", ")
}
