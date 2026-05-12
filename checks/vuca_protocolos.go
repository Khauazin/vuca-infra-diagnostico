package checks

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
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
	// Le pelo menos 7 bytes (frame header) — suficiente para distinguir
	// entre Connection.Start (frame method) e Version negotiation (8 bytes
	// "AMQP\x00\x00\xMM\xmm" quando broker rejeita versao). ReadAtLeast
	// nao bloqueia esperando o buffer encher: sai quando n >= 7.
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
		// Avalia tempo do handshake (C — Timing)
		totalHandshake := durConn + durSend + durRead
		statusTiming := StatusOK
		mensagemFinal := "AMQP responde normalmente — servidor RabbitMQ saudavel"
		detalheHandshake := fmt.Sprintf("Recebeu Connection.Start (classe=%d, metodo=%d, %d bytes) em %dms", classe, metodo, n, totalHandshake)
		switch {
		case totalHandshake >= 2000:
			statusTiming = StatusFail
			mensagemFinal = fmt.Sprintf("AMQP responde mas extremamente lento (%dms para handshake) — broker sobrecarregado ou rede com problemas serios", totalHandshake)
			detalheHandshake += " — CRITICO: > 2000ms"
		case totalHandshake >= 500:
			statusTiming = StatusWarn
			mensagemFinal = fmt.Sprintf("AMQP responde mas lento (%dms para handshake) — pode causar atraso em conexoes de impressao", totalHandshake)
			detalheHandshake += " — lento: > 500ms"
		}
		add(SubPasso{
			Descricao: "Aguardar resposta do servidor (Connection.Start)",
			Status:    statusTiming,
			DuracaoMs: durRead,
			Detalhe:   detalheHandshake,
		})
		r.SubPassos = subpassos
		r.Status = statusTiming
		r.Mensagem = mensagemFinal
		r.Detalhes = map[string]interface{}{
			"classe":          classe,
			"metodo":          metodo,
			"bytes_recebidos": n,
			"handshake_ms":    totalHandshake,
			"tcp_ms":          durConn,
			"send_ms":         durSend,
			"recv_ms":         durRead,
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

// ============================================================================
// CHECKS DA MANAGEMENT API DO RABBITMQ (porta 15672)
// ============================================================================

// rabbitMQMgmtURL monta a URL base da Management API.
func rabbitMQMgmtURL(host string) string {
	return fmt.Sprintf("http://%s:15672", host)
}

// rabbitMQAuthHeader monta o header Authorization Basic para a Management API.
// Retorna string vazia se usuario/senha estiverem vazios.
func rabbitMQAuthHeader(usuario, senha string) string {
	if usuario == "" {
		return ""
	}
	cred := base64.StdEncoding.EncodeToString([]byte(usuario + ":" + senha))
	return "Basic " + cred
}

// CheckRabbitMQManagement valida que a porta 15672 esta servindo a Management
// API real do RabbitMQ (nao um proxy ou outro servico). Faz GET /api/overview:
//   - Sem auth: aceita 401 (significa que existe e pediu auth — e RabbitMQ)
//   - Com auth: valida que retorna 200 com info do broker
//
// Identifica versao do broker, cluster name, Erlang version.
func CheckRabbitMQManagement(host, usuario, senha string, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "RabbitMQ", Nome: "Management API (porta 15672)"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	inicio := time.Now()
	mgmtURL := rabbitMQMgmtURL(host) + "/api/overview"

	client := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest("GET", mgmtURL, nil)
	if header := rabbitMQAuthHeader(usuario, senha); header != "" {
		req.Header.Set("Authorization", header)
	}

	tReq := time.Now()
	resp, err := client.Do(req)
	durReq := time.Since(tReq).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: fmt.Sprintf("Requisitar %s", mgmtURL),
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Management API inacessivel: %s", err.Error())
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16384))
	add(SubPasso{
		Descricao: fmt.Sprintf("Requisitar %s", mgmtURL),
		Status:    StatusOK,
		DuracaoMs: durReq,
		Detalhe:   fmt.Sprintf("HTTP %d, %d bytes recebidos", resp.StatusCode, len(body)),
	})

	r.Detalhes = map[string]interface{}{
		"url":         mgmtURL,
		"status_code": resp.StatusCode,
	}

	// Sem credenciais: 401 e' o esperado (RabbitMQ existe mas pediu auth)
	if usuario == "" {
		switch {
		case resp.StatusCode == 401:
			add(SubPasso{
				Descricao: "Confirmar que a porta serve a Management API",
				Status:    StatusOK,
				Detalhe:   "HTTP 401 — Management API ativa, exige credenciais para mais detalhes",
			})
			r.Status = StatusOK
			r.Mensagem = "Management API ativa (porta 15672). Preencha usuario+senha para inspecionar queues."
		case resp.StatusCode == 200:
			r.Status = StatusOK
			r.Mensagem = "Management API acessivel sem autenticacao (configuracao incomum mas valida)"
		default:
			r.Status = StatusWarn
			r.Mensagem = fmt.Sprintf("Resposta inesperada %d — pode nao ser RabbitMQ Management", resp.StatusCode)
		}
		r.SubPassos = subpassos
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	// Com credenciais: 200 esperado, 401 = senha errada
	if resp.StatusCode == 401 {
		add(SubPasso{
			Descricao: "Validar credenciais admin",
			Status:    StatusFail,
			Detalhe:   "HTTP 401 — usuario ou senha incorretos",
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Credenciais admin invalidas (usuario ou senha incorretos)"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	if resp.StatusCode != 200 {
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Resposta inesperada %d da Management API", resp.StatusCode)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	// Parseia o overview
	var overview struct {
		RabbitVersion    string `json:"rabbitmq_version"`
		ErlangVersion    string `json:"erlang_version"`
		ClusterName      string `json:"cluster_name"`
		ManagementVersion string `json:"management_version"`
	}
	_ = json.Unmarshal(body, &overview)
	add(SubPasso{
		Descricao: "Validar credenciais admin e inspecionar broker",
		Status:    StatusOK,
		Detalhe: fmt.Sprintf("Autenticado · RabbitMQ %s · Erlang %s · cluster %s",
			overview.RabbitVersion, overview.ErlangVersion, overview.ClusterName),
	})

	r.Detalhes["rabbitmq_version"] = overview.RabbitVersion
	r.Detalhes["erlang_version"] = overview.ErlangVersion
	r.Detalhes["cluster_name"] = overview.ClusterName
	r.Status = StatusOK
	r.Mensagem = fmt.Sprintf("Management API OK · RabbitMQ %s no cluster '%s'", overview.RabbitVersion, overview.ClusterName)
	r.SubPassos = subpassos
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// queueRabbitMQ representa o subset de informacoes que importam de uma queue
// retornada pela Management API.
type queueRabbitMQ struct {
	Name        string `json:"name"`
	Vhost       string `json:"vhost"`
	Messages    int    `json:"messages"`
	Consumers   int    `json:"consumers"`
	State       string `json:"state"`
}

// CheckRabbitMQQueues lista todas as queues do vhost informado e filtra as do
// padrao `vucaprint_{instancia}_*`. Para cada uma, reporta: nome, mensagens em
// fila, consumers ativos, estado. Quando o tecnico fornece IDs das impressoras
// (Unidade + Impressora), correlaciona impressora local <-> queue na nuvem.
//
// Status:
//   - INFO: queues listadas, sem problemas detectados
//   - WARN: alguma queue sem consumer OU acumulando mensagens
//   - FAIL: vhost nao acessivel OU instancia sem nenhuma queue (provisionamento ruim)
func CheckRabbitMQQueues(host, usuario, senha, instancia, vhost string, impressoras []Impressora, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "RabbitMQ", Nome: "Queues do cliente"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	if instancia == "" {
		add(SubPasso{
			Descricao: "Validar instancia informada",
			Status:    StatusInfo,
			Detalhe:   "Instancia nao informada — sem como identificar queues do cliente",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Instancia nao informada — pulando"
		return r
	}
	if usuario == "" {
		add(SubPasso{
			Descricao: "Validar credenciais informadas",
			Status:    StatusInfo,
			Detalhe:   "Usuario/senha admin nao informados — Management API exige autenticacao",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Credenciais admin nao informadas — pulando inspecao de queues"
		return r
	}

	inicio := time.Now()
	vhostUsado := vhost
	if vhostUsado == "" {
		vhostUsado = "/"
	}
	vhostEnc := url.PathEscape(vhostUsado)
	if vhostEnc == "/" {
		vhostEnc = "%2F"
	}
	queuesURL := rabbitMQMgmtURL(host) + "/api/queues/" + vhostEnc

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", queuesURL, nil)
	req.Header.Set("Authorization", rabbitMQAuthHeader(usuario, senha))

	tReq := time.Now()
	resp, err := client.Do(req)
	durReq := time.Since(tReq).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: fmt.Sprintf("Listar queues do vhost '%s'", vhostUsado),
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Falha ao consultar queues: " + err.Error()
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 401 {
		add(SubPasso{
			Descricao: fmt.Sprintf("Listar queues do vhost '%s'", vhostUsado),
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   "HTTP 401 — credenciais sem permissao no vhost",
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Credenciais sem permissao no vhost"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	if resp.StatusCode == 404 {
		add(SubPasso{
			Descricao: fmt.Sprintf("Listar queues do vhost '%s'", vhostUsado),
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   fmt.Sprintf("Vhost '%s' nao existe no broker", vhostUsado),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Vhost '%s' nao existe", vhostUsado)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	if resp.StatusCode != 200 {
		add(SubPasso{
			Descricao: fmt.Sprintf("Listar queues do vhost '%s'", vhostUsado),
			Status:    StatusWarn,
			DuracaoMs: durReq,
			Detalhe:   fmt.Sprintf("HTTP %d inesperado", resp.StatusCode),
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Resposta inesperada %d", resp.StatusCode)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	var todasQueues []queueRabbitMQ
	if err := json.Unmarshal(body, &todasQueues); err != nil {
		add(SubPasso{
			Descricao: fmt.Sprintf("Listar queues do vhost '%s'", vhostUsado),
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   "Falha ao parsear JSON: " + err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Resposta da Management API invalida"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: fmt.Sprintf("Listar queues do vhost '%s'", vhostUsado),
		Status:    StatusOK,
		DuracaoMs: durReq,
		Detalhe:   fmt.Sprintf("%d queues no vhost (broker inteiro)", len(todasQueues)),
	})

	// Filtra pelo padrao vucaprint_{instancia}_*
	prefixo := "vucaprint_" + instancia + "_"
	queuesCliente := []queueRabbitMQ{}
	for _, q := range todasQueues {
		if strings.HasPrefix(q.Name, prefixo) {
			queuesCliente = append(queuesCliente, q)
		}
	}
	sort.Slice(queuesCliente, func(i, j int) bool { return queuesCliente[i].Name < queuesCliente[j].Name })

	r.Detalhes = map[string]interface{}{
		"vhost":          vhostUsado,
		"total_queues":   len(todasQueues),
		"queues_cliente": queuesCliente,
		"padrao_busca":   prefixo + "*",
	}

	if len(queuesCliente) == 0 {
		add(SubPasso{
			Descricao: fmt.Sprintf("Filtrar queues com prefixo '%s'", prefixo),
			Status:    StatusFail,
			Detalhe:   "NENHUMA queue encontrada para esta instancia — cliente nao foi provisionado no broker",
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Nenhuma queue '%s*' encontrada — cliente nao provisionado no RabbitMQ", prefixo)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	// Analisa cada queue do cliente
	var semConsumer, acumulando []string
	for _, q := range queuesCliente {
		detalheQueue := fmt.Sprintf("%d msgs · %d consumer(s) · estado %s", q.Messages, q.Consumers, q.State)
		statusQueue := StatusOK
		if q.Consumers == 0 {
			statusQueue = StatusWarn
			semConsumer = append(semConsumer, q.Name)
			detalheQueue += " · SEM CONSUMER (VucaPrint local desconectado?)"
		}
		if q.Messages >= 50 {
			if statusQueue != StatusWarn {
				statusQueue = StatusWarn
			}
			acumulando = append(acumulando, fmt.Sprintf("%s (%d msgs)", q.Name, q.Messages))
		}
		add(SubPasso{
			Descricao: "Queue " + q.Name,
			Status:    statusQueue,
			Detalhe:   detalheQueue,
		})
	}

	// Correlacao opcional com impressoras locais (Caminho 3)
	correlacionadas := 0
	for _, imp := range impressoras {
		if imp.UnidadeID == "" || imp.ImpressoraID == "" {
			continue
		}
		correlacionadas++
		queueEsperada := fmt.Sprintf("vucaprint_%s_%s_%s", instancia, imp.UnidadeID, imp.ImpressoraID)
		var encontrada *queueRabbitMQ
		for i := range queuesCliente {
			if queuesCliente[i].Name == queueEsperada {
				encontrada = &queuesCliente[i]
				break
			}
		}
		nomeImp := imp.Nome
		if nomeImp == "" {
			nomeImp = imp.IP
		}
		if encontrada == nil {
			add(SubPasso{
				Descricao: fmt.Sprintf("Correlacao: impressora '%s' (Unidade %s, Impressora %s)", nomeImp, imp.UnidadeID, imp.ImpressoraID),
				Status:    StatusFail,
				Detalhe: fmt.Sprintf("Queue esperada '%s' NAO existe no broker — provisionamento incorreto",
					queueEsperada),
			})
			continue
		}
		statusCorr := StatusOK
		detalheCorr := fmt.Sprintf("Queue %s · %d msg · %d consumer(s)",
			encontrada.Name, encontrada.Messages, encontrada.Consumers)
		if encontrada.Consumers == 0 {
			statusCorr = StatusWarn
			detalheCorr += " · VucaPrint local pode estar desconectado para esta impressora"
		}
		add(SubPasso{
			Descricao: fmt.Sprintf("Correlacao: impressora '%s' (%s)", nomeImp, queueEsperada),
			Status:    statusCorr,
			Detalhe:   detalheCorr,
		})
	}

	// Status consolidado
	statusFinal := StatusOK
	mensagens := []string{fmt.Sprintf("%d queues encontradas para '%s'", len(queuesCliente), instancia)}
	if len(semConsumer) > 0 {
		statusFinal = StatusWarn
		mensagens = append(mensagens, fmt.Sprintf("%d sem consumer (jobs nao serao consumidos)", len(semConsumer)))
	}
	if len(acumulando) > 0 {
		statusFinal = StatusWarn
		mensagens = append(mensagens, fmt.Sprintf("%d acumulando mensagens", len(acumulando)))
	}
	if correlacionadas > 0 {
		mensagens = append(mensagens, fmt.Sprintf("%d impressora(s) correlacionada(s) com queues", correlacionadas))
	}

	r.SubPassos = subpassos
	r.Status = statusFinal
	r.Mensagem = strings.Join(mensagens, " · ")
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckRabbitMQHeartbeat abre uma conexao TCP+AMQP no broker e a mantem ociosa
// por 10 segundos para detectar drop de conexoes idle por NAT/firewall do
// cliente. Mid-tempo causa do "tablet desconecta sozinho do RabbitMQ".
//
// Status:
//   - OK: conexao sobreviveu 10s sem ser derrubada
//   - WARN: conexao caiu durante o periodo idle
//   - FAIL: nao conseguiu nem conectar
func CheckRabbitMQHeartbeat(host string, porta int, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "RabbitMQ", Nome: "Estabilidade da conexao (10s idle)"}
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
			Descricao: fmt.Sprintf("Abrir conexao TCP em %s", endereco),
			Status:    StatusFail,
			DuracaoMs: durConn,
			Detalhe:   normalizaErroRede(err),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Nao foi possivel conectar para teste de estabilidade"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer conn.Close()
	add(SubPasso{
		Descricao: fmt.Sprintf("Abrir conexao TCP em %s", endereco),
		Status:    StatusOK,
		DuracaoMs: durConn,
		Detalhe:   "Conexao aberta · iniciando periodo de 10s ocioso",
	})

	// Envia header AMQP para abrir conexao real (mais realista que TCP nu)
	header := []byte{'A', 'M', 'Q', 'P', 0x00, 0x00, 0x09, 0x01}
	if _, err := conn.Write(header); err == nil {
		respBuf := make([]byte, 64)
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.ReadAtLeast(conn, respBuf, 7) // descarta a Connection.Start
		conn.SetReadDeadline(time.Time{})       // reseta deadline
	}

	// Aguarda 10s ocioso. Durante esse tempo, NAT/firewall do cliente pode
	// derrubar conexoes idle silenciosamente.
	time.Sleep(10 * time.Second)

	// Tenta ler 1 byte com timeout curto. Se a conexao foi derrubada
	// remotamente, vamos receber EOF ou erro de connection reset.
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	probe := make([]byte, 1)
	_, errProbe := conn.Read(probe)
	conn.SetReadDeadline(time.Time{})

	caiu := false
	motivo := ""
	if errProbe != nil {
		msg := errProbe.Error()
		// "i/o timeout" = nao recebeu nada mas conexao parece viva (esperado)
		if strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded") {
			caiu = false
		} else if strings.Contains(msg, "EOF") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "broken pipe") {
			caiu = true
			motivo = "Conexao foi derrubada pelo servidor/firewall durante o idle"
		}
	}

	// Tenta escrever 1 byte (heartbeat AMQP-like) — se a conexao foi derrubada
	// por NAT silenciosamente, write tambem vai falhar
	if !caiu {
		heartbeat := []byte{0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xCE}
		if _, err := conn.Write(heartbeat); err != nil {
			caiu = true
			motivo = "Conexao quebrou no envio de heartbeat: " + err.Error()
		}
	}

	if caiu {
		add(SubPasso{
			Descricao: "Validar conexao apos 10s de inatividade",
			Status:    StatusWarn,
			Detalhe:   motivo,
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = "Conexao foi derrubada durante 10s ocioso — NAT/firewall com timeout curto"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	add(SubPasso{
		Descricao: "Validar conexao apos 10s de inatividade",
		Status:    StatusOK,
		Detalhe:   "Conexao sobreviveu 10s ociosa — NAT/firewall nao esta derrubando idle",
	})
	r.SubPassos = subpassos
	r.Status = StatusOK
	r.Mensagem = "Conexao estavel apos 10s — NAT/firewall nao derruba sessoes ociosas"
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}
