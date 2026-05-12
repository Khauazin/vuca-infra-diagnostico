package checks

import (
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Variaveis de pacote para URLs externas — extraidas para permitir testes.
var (
	urlBanda           = "https://speed.cloudflare.com/__down?bytes=10000000"
	urlTraceCloudflare = "https://cloudflare.com/cdn-cgi/trace"
)

// CheckIPPublico identifica o IP publico de saida do cliente e o datacenter
// Cloudflare mais proximo. Util pra o tecnico identificar a operadora/ISP
// quando aparecem problemas recorrentes ("ah, e cliente em Vivo Fibra de
// novo"). Tambem ajuda a confirmar que a saida pra internet esta funcionando.
func CheckIPPublico(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Rede Local", Nome: "Identidade externa (IP publico)"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	inicio := time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	tReq := time.Now()
	resp, err := client.Get(urlTraceCloudflare)
	durReq := time.Since(tReq).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: "Consultar endpoint publico de identidade (Cloudflare)",
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = "Nao foi possivel identificar IP publico — " + err.Error()
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	add(SubPasso{
		Descricao: "Consultar endpoint publico de identidade (Cloudflare)",
		Status:    StatusOK,
		DuracaoMs: durReq,
		Detalhe:   fmt.Sprintf("HTTP %d, %d bytes recebidos", resp.StatusCode, len(bodyBytes)),
	})

	info := map[string]string{}
	for _, linha := range strings.Split(string(bodyBytes), "\n") {
		partes := strings.SplitN(linha, "=", 2)
		if len(partes) == 2 {
			info[partes[0]] = partes[1]
		}
	}

	ipPublico := info["ip"]
	if ipPublico == "" {
		add(SubPasso{
			Descricao: "Identificar IP publico de saida",
			Status:    StatusWarn,
			Detalhe:   "Resposta nao continha campo 'ip'",
		})
	} else {
		add(SubPasso{
			Descricao: "Identificar IP publico de saida",
			Status:    StatusOK,
			Detalhe:   ipPublico,
		})
	}

	colo := info["colo"]
	if colo != "" {
		add(SubPasso{
			Descricao: "Identificar datacenter Cloudflare mais proximo",
			Status:    StatusInfo,
			Detalhe:   fmt.Sprintf("Codigo %s (indicacao da geografia/operadora de saida)", colo),
		})
	}

	loc := info["loc"]
	if loc != "" {
		add(SubPasso{
			Descricao: "Identificar pais detectado",
			Status:    StatusInfo,
			Detalhe:   loc,
		})
	}

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"info_completa": info,
	}
	r.Status = StatusInfo
	if ipPublico != "" {
		r.Mensagem = fmt.Sprintf("IP publico: %s · Cloudflare CDN: %s (%s)", ipPublico, colo, loc)
	} else {
		r.Mensagem = "Identidade externa parcialmente identificada"
	}
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckGatewayLocal identifica o gateway padrao do sistema e mede latencia
// fazendo conexoes TCP em portas comumente abertas em roteadores domesticos
// (53/DNS, 80/admin, 443/admin). Latencia alta no gateway e' sintoma classico
// de roteador sobrecarregado ou com defeito.
func CheckGatewayLocal(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Rede Local", Nome: "Gateway local (roteador)"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	inicio := time.Now()

	gw := gatewayPadrao()
	if gw == "" {
		add(SubPasso{
			Descricao: "Identificar gateway padrao do sistema",
			Status:    StatusInfo,
			Detalhe:   "Nao implementado neste SO ou sem gateway configurado",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Gateway nao identificado neste sistema"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: "Identificar gateway padrao do sistema",
		Status:    StatusOK,
		Detalhe:   fmt.Sprintf("Gateway: %s", gw),
	})

	// Testa portas comuns. Quando uma responder, faz 4 amostras adicionais
	// para ter um numero util de latencia.
	portas := []int{53, 80, 443}
	var portaOk int
	var amostras []int64
	for _, p := range portas {
		endereco := net.JoinHostPort(gw, strconv.Itoa(p))
		t0 := time.Now()
		conn, err := net.DialTimeout("tcp", endereco, 1*time.Second)
		durFirst := time.Since(t0).Milliseconds()
		if err != nil {
			continue
		}
		conn.Close()
		portaOk = p
		amostras = []int64{durFirst}
		for i := 0; i < 4; i++ {
			t1 := time.Now()
			c2, e2 := net.DialTimeout("tcp", endereco, 1*time.Second)
			d2 := time.Since(t1).Milliseconds()
			if e2 == nil {
				c2.Close()
				amostras = append(amostras, d2)
			}
		}
		break
	}

	if portaOk == 0 {
		add(SubPasso{
			Descricao: fmt.Sprintf("Testar conectividade TCP no gateway (%s)", gw),
			Status:    StatusInfo,
			Detalhe:   "Gateway nao respondeu em portas comuns (53/80/443) — normal em alguns roteadores que bloqueiam acesso a admin",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = fmt.Sprintf("Gateway %s identificado mas nao respondeu em portas comuns (sem indicio de problema)", gw)
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	var soma, max int64
	var min int64 = -1
	for _, a := range amostras {
		soma += a
		if a > max {
			max = a
		}
		if min < 0 || a < min {
			min = a
		}
	}
	media := soma / int64(len(amostras))

	statusFinal := StatusOK
	detalhe := fmt.Sprintf("min %dms · media %dms · max %dms (TCP/%d, %d amostras)", min, media, max, portaOk, len(amostras))
	mensagem := fmt.Sprintf("Gateway %s respondeu rapido — media %dms", gw, media)
	switch {
	case media > 100:
		statusFinal = StatusWarn
		detalhe += " — gateway lento, pode indicar roteador sobrecarregado ou com defeito"
		mensagem = fmt.Sprintf("Aviso: gateway %s lento — media %dms (sintoma de roteador sobrecarregado)", gw, media)
	case media > 50:
		statusFinal = StatusWarn
		detalhe += " — gateway com latencia acima do esperado"
		mensagem = fmt.Sprintf("Aviso: gateway %s com latencia elevada — media %dms", gw, media)
	}
	add(SubPasso{
		Descricao: fmt.Sprintf("Medir latencia no gateway via TCP/%d (5 amostras)", portaOk),
		Status:    statusFinal,
		DuracaoMs: media,
		Detalhe:   detalhe,
	})

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"gateway":       gw,
		"porta_testada": portaOk,
		"media_ms":      media,
		"min_ms":        min,
		"max_ms":        max,
		"amostras":      amostras,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckRedeLocalInfo lista as interfaces de rede ativas, com IP, mascara e
// CIDR. Util pra o tecnico identificar a estrutura da rede (VLANs, /24 vs /22,
// etc) e detectar mascara mal configurada.
func CheckRedeLocalInfo(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Rede Local", Nome: "Interfaces e enderecamento IP"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}
	inicio := time.Now()

	interfaces, err := net.Interfaces()
	if err != nil {
		add(SubPasso{
			Descricao: "Listar interfaces de rede do sistema",
			Status:    StatusFail,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Falha ao listar interfaces: " + err.Error()
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: "Listar interfaces de rede do sistema",
		Status:    StatusOK,
		Detalhe:   fmt.Sprintf("%d interfaces detectadas no SO", len(interfaces)),
	})

	interfacesAtivas := []map[string]interface{}{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, errA := iface.Addrs()
		if errA != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ipNet.IP.To4() == nil {
				// IPv6 — pula nesse check (so' IPv4 aqui)
				continue
			}
			cidr, _ := ipNet.Mask.Size()
			mascaraStr := net.IP(ipNet.Mask).String()
			info := map[string]interface{}{
				"interface": iface.Name,
				"ip":        ipNet.IP.String(),
				"mascara":   mascaraStr,
				"cidr":      cidr,
			}
			interfacesAtivas = append(interfacesAtivas, info)
			add(SubPasso{
				Descricao: fmt.Sprintf("Interface %s", iface.Name),
				Status:    StatusInfo,
				Detalhe:   fmt.Sprintf("IP %s/%d (mascara %s)", ipNet.IP.String(), cidr, mascaraStr),
			})
		}
	}

	if len(interfacesAtivas) == 0 {
		add(SubPasso{
			Descricao: "Identificar interface ativa com IPv4",
			Status:    StatusWarn,
			Detalhe:   "Nenhuma interface ativa com IPv4 — sintoma de rede desconectada",
		})
		r.SubPassos = subpassos
		r.Status = StatusWarn
		r.Mensagem = "Nenhuma interface de rede ativa com IPv4"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"interfaces": interfacesAtivas,
	}
	r.Status = StatusInfo
	r.Mensagem = fmt.Sprintf("%d interface(s) ativa(s) com IPv4 detectada(s)", len(interfacesAtivas))
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckBanda mede a banda de download baixando 10MB do speed test publico do
// Cloudflare. Util pra estimar se a internet do cliente aguenta varios tablets
// simultaneos. Endpoint nao requer auth e e' mantido pela Cloudflare como
// servico publico de speed test.
func CheckBanda(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Rede Local", Nome: "Banda de download (10MB)"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}
	inicio := time.Now()

	url := urlBanda
	client := &http.Client{Timeout: 60 * time.Second}

	tReq := time.Now()
	resp, err := client.Get(url)
	durReq := time.Since(tReq).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: "Iniciar download do speed test (Cloudflare)",
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Falha ao iniciar download do speed test: " + err.Error()
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer resp.Body.Close()
	add(SubPasso{
		Descricao: "Iniciar download do speed test (Cloudflare)",
		Status:    StatusOK,
		DuracaoMs: durReq,
		Detalhe:   fmt.Sprintf("HTTP %d — iniciando transferencia de 10MB", resp.StatusCode),
	})

	tDown := time.Now()
	bytesLidos, errCopy := io.Copy(io.Discard, resp.Body)
	durDown := time.Since(tDown).Milliseconds()
	if errCopy != nil {
		add(SubPasso{
			Descricao: "Concluir download de 10MB",
			Status:    StatusFail,
			DuracaoMs: durDown,
			Detalhe:   fmt.Sprintf("%d bytes recebidos antes de erro: %s", bytesLidos, errCopy.Error()),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Conexao caiu durante download: " + errCopy.Error()
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: "Concluir download de 10MB",
		Status:    StatusOK,
		DuracaoMs: durDown,
		Detalhe:   fmt.Sprintf("%d bytes recebidos", bytesLidos),
	})

	// Calcula throughput em Mbps.
	segundos := float64(durDown) / 1000.0
	if segundos <= 0 {
		segundos = 0.001
	}
	mbps := (float64(bytesLidos) * 8.0) / (segundos * 1_000_000.0)

	statusFinal := StatusOK
	mensagem := fmt.Sprintf("Banda OK: %.1f Mbps (%dMB em %.1fs)", mbps, bytesLidos/1_000_000, segundos)
	switch {
	case mbps < 3:
		statusFinal = StatusFail
		mensagem = fmt.Sprintf("Banda critica: %.1f Mbps — nao suporta operacao com varios dispositivos", mbps)
	case mbps < 10:
		statusFinal = StatusWarn
		mensagem = fmt.Sprintf("Banda limitada: %.1f Mbps — pode funcionar mas com gargalo em horario de pico", mbps)
	}
	add(SubPasso{
		Descricao: "Calcular velocidade efetiva",
		Status:    statusFinal,
		DuracaoMs: durDown,
		Detalhe:   fmt.Sprintf("%.1f Mbps (%.2f MB/s)", mbps, mbps/8.0),
	})

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"url":            url,
		"bytes_recebidos": bytesLidos,
		"duracao_ms":     durDown,
		"mbps":           mbps,
		"mb_por_segundo": mbps / 8.0,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckLatenciaLonga roda 50 amostras TCP no host da instancia ao longo de 30s.
// Detecta jitter alto e perda intermitente — sintomas de saturacao de WiFi,
// uplink instavel ou operadora derrubando idle.
func CheckLatenciaLonga(instancia string, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Rede Local", Nome: "Latencia estendida (50 amostras / 30s)"}
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
			Detalhe:   "Instancia nao informada — pulando",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Instancia nao informada — pulando"
		return r
	}

	host := construtorHostInstancia443(instancia)
	const totalAmostras = 50
	const duracaoTotal = 30 * time.Second
	intervalo := duracaoTotal / time.Duration(totalAmostras)

	inicio := time.Now()
	add(SubPasso{
		Descricao: fmt.Sprintf("Iniciar 50 amostras TCP em %s ao longo de 30s", host),
		Status:    StatusInfo,
		Detalhe:   fmt.Sprintf("1 amostra a cada %dms", intervalo.Milliseconds()),
	})

	var amostras []int64
	var falhas int
	for i := 0; i < totalAmostras; i++ {
		t0 := time.Now()
		conn, err := net.DialTimeout("tcp", host, 2*time.Second)
		dur := time.Since(t0).Milliseconds()
		if err == nil {
			conn.Close()
			amostras = append(amostras, dur)
		} else {
			falhas++
		}
		// Espera o intervalo (compensando o tempo gasto).
		time.Sleep(intervalo - time.Duration(dur)*time.Millisecond)
	}

	perda := float64(falhas) / float64(totalAmostras) * 100
	media, min, max, jitter := estatisticas(amostras)

	add(SubPasso{
		Descricao: "Coletar resultados das 50 amostras",
		Status:    StatusOK,
		DuracaoMs: time.Since(inicio).Milliseconds(),
		Detalhe:   fmt.Sprintf("%d sucessos, %d falhas (perda %.1f%%)", len(amostras), falhas, perda),
	})

	statusFinal := StatusOK
	mensagem := fmt.Sprintf("Rede estavel — media %dms, jitter %dms, perda %.0f%%", media, jitter, perda)
	switch {
	case perda > 5 || jitter > 200:
		statusFinal = StatusFail
		mensagem = fmt.Sprintf("Instabilidade critica: jitter %dms, perda %.1f%% (media %dms, min %dms, max %dms) — rede ou WiFi saturado, vai causar travamentos", jitter, perda, media, min, max)
	case perda > 1 || jitter > 50:
		statusFinal = StatusWarn
		mensagem = fmt.Sprintf("Instabilidade detectada: jitter %dms, perda %.1f%% (media %dms, min %dms, max %dms) — sintoma de WiFi saturado, uplink ruim, ou operadora derrubando conexoes", jitter, perda, media, min, max)
	}

	add(SubPasso{
		Descricao: "Avaliar jitter e perda de pacote",
		Status:    statusFinal,
		Detalhe:   fmt.Sprintf("media %dms, min %dms, max %dms, jitter (stddev) %dms, perda %.1f%%", media, min, max, jitter, perda),
	})

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"host":         host,
		"amostras":     totalAmostras,
		"sucessos":     len(amostras),
		"falhas":       falhas,
		"perda_pct":    perda,
		"media_ms":     media,
		"min_ms":       min,
		"max_ms":       max,
		"jitter_ms":    jitter,
		"todas_amostras": amostras,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// estatisticas calcula media, min, max e jitter (desvio padrao) de uma slice
// de latencias em ms.
func estatisticas(amostras []int64) (media, min, max, jitter int64) {
	if len(amostras) == 0 {
		return 0, 0, 0, 0
	}
	var soma int64
	min = amostras[0]
	max = amostras[0]
	for _, a := range amostras {
		soma += a
		if a < min {
			min = a
		}
		if a > max {
			max = a
		}
	}
	media = soma / int64(len(amostras))
	var somaQuadrados float64
	for _, a := range amostras {
		diff := float64(a - media)
		somaQuadrados += diff * diff
	}
	jitter = int64(math.Sqrt(somaQuadrados / float64(len(amostras))))
	return media, min, max, jitter
}

// CheckMTU descobre o MTU efetivo da rede ate o gateway usando ping com flag
// "no fragmentar" (DF). MTU < 1500 e' sintoma classico de PPPoE mal configurado
// e causa lentidao silenciosa em uploads. So funciona no Windows (depende do
// `ping.exe`).
func CheckMTU(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Rede Local", Nome: "MTU / Fragmentacao"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}
	inicio := time.Now()

	if runtime.GOOS != "windows" {
		add(SubPasso{
			Descricao: "Validar SO compatible",
			Status:    StatusInfo,
			Detalhe:   "Implementado apenas em Windows por enquanto",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "MTU test pulado — implementado apenas em Windows"
		return r
	}

	gw := gatewayPadrao()
	if gw == "" {
		add(SubPasso{
			Descricao: "Identificar gateway de destino",
			Status:    StatusInfo,
			Detalhe:   "Sem gateway — usando 1.1.1.1 como alternativa",
		})
		gw = "1.1.1.1"
	} else {
		add(SubPasso{
			Descricao: "Identificar gateway de destino",
			Status:    StatusOK,
			Detalhe:   gw,
		})
	}

	// Testa MTU de 1500 para baixo. Payload = MTU - 28 (IP=20 + ICMP=8).
	mtusParaTestar := []int{1500, 1400, 1300, 1200, 1100, 1000}
	var mtuEfetivo int
	for _, mtu := range mtusParaTestar {
		payload := mtu - 28
		tTest := time.Now()
		ok := tentarMTU(gw, payload)
		dur := time.Since(tTest).Milliseconds()
		if ok {
			mtuEfetivo = mtu
			add(SubPasso{
				Descricao: fmt.Sprintf("Testar MTU %d (payload %d bytes, no-fragment)", mtu, payload),
				Status:    StatusOK,
				DuracaoMs: dur,
				Detalhe:   "Pacote passou sem fragmentacao",
			})
			break
		}
		add(SubPasso{
			Descricao: fmt.Sprintf("Testar MTU %d (payload %d bytes, no-fragment)", mtu, payload),
			Status:    StatusInfo,
			DuracaoMs: dur,
			Detalhe:   "Bloqueado (precisaria fragmentar) ou timeout",
		})
	}

	statusFinal := StatusOK
	mensagem := fmt.Sprintf("MTU 1500 funciona normalmente (testado em %s)", gw)
	if mtuEfetivo == 0 {
		statusFinal = StatusWarn
		mensagem = fmt.Sprintf("Nao foi possivel determinar MTU (gateway %s nao respondeu a ICMP, ou todos os tamanhos falharam)", gw)
	} else if mtuEfetivo < 1500 {
		statusFinal = StatusWarn
		mensagem = fmt.Sprintf("MTU reduzido detectado: %d (esperado 1500). Causa classica: PPPoE/VPN configurado errado — gera lentidao silenciosa em uploads grandes", mtuEfetivo)
	}

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"gateway":      gw,
		"mtu_efetivo":  mtuEfetivo,
		"mtus_testados": mtusParaTestar,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// tentarMTU executa um ping no Windows com tamanho especifico e flag
// "no fragmentar" (-f). Retorna true se o destino respondeu (TTL aparece no
// output), false se foi bloqueado ou timeout. Funciona em pt-BR e en-US
// porque o marcador "TTL=" e' ASCII e aparece nos dois idiomas.
func tentarMTU(host string, payloadBytes int) bool {
	cmd := exec.Command("ping", "-n", "1", "-f", "-l", strconv.Itoa(payloadBytes), "-w", "2000", host)
	out, _ := cmd.CombinedOutput()
	return strings.Contains(strings.ToUpper(string(out)), "TTL=")
}

// gatewayPadrao retorna o IP do gateway padrao do sistema. So implementado
// no Windows por enquanto (parse de `ipconfig`). Em outros SOs, retorna "".
func gatewayPadrao() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	cmd := exec.Command("ipconfig")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return parseGatewayIpconfig(string(out))
}

// parseGatewayIpconfig procura por linhas "Default Gateway" ou "Gateway Padrao"
// no output do ipconfig e extrai o primeiro IP encontrado.
func parseGatewayIpconfig(saida string) string {
	saida = strings.ReplaceAll(saida, "\r", "")
	linhas := strings.Split(saida, "\n")
	for _, linha := range linhas {
		l := strings.TrimSpace(linha)
		minusc := strings.ToLower(l)
		if strings.Contains(minusc, "default gateway") || strings.Contains(minusc, "gateway padr") || strings.Contains(minusc, "gateway padrão") {
			if idx := strings.LastIndex(l, ":"); idx > 0 {
				candidato := strings.TrimSpace(l[idx+1:])
				if candidato != "" && reIPLiteral.MatchString(candidato) {
					return candidato
				}
			}
		}
	}
	return ""
}
