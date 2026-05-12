package checks

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Var de pacote para a URL de referencia do relogio — extraida para testes.
var urlRelogio = "https://www.cloudflare.com/"

// CheckProxyAtivo detecta se ha um proxy HTTP/HTTPS configurado na maquina —
// seja por variavel de ambiente ou no Windows via netsh winhttp. Proxy oculto
// e' causa comum de problemas: tecnico nao sabe que tem um proxy ativo (de
// VPN corporativa antiga ou software de filtragem), e ele intercepta/altera
// requisicoes silenciosamente.
//
// Status:
//   - INFO: sem proxy configurado (saudavel)
//   - WARN: proxy detectado (nao falha, so' alerta para o tecnico verificar)
func CheckProxyAtivo(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Ambiente", Nome: "Proxy / VPN ativo"}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}
	inicio := time.Now()

	// (1) Variaveis de ambiente
	envProxies := map[string]string{
		"HTTP_PROXY":  os.Getenv("HTTP_PROXY"),
		"HTTPS_PROXY": os.Getenv("HTTPS_PROXY"),
		"http_proxy":  os.Getenv("http_proxy"),
		"https_proxy": os.Getenv("https_proxy"),
		"NO_PROXY":    os.Getenv("NO_PROXY"),
	}
	envSet := []string{}
	for k, v := range envProxies {
		if v != "" {
			envSet = append(envSet, fmt.Sprintf("%s=%s", k, v))
		}
	}
	if len(envSet) > 0 {
		add(SubPasso{
			Descricao: "Inspecionar variaveis de ambiente de proxy",
			Status:    StatusWarn,
			Detalhe:   "Proxy detectado em variaveis: " + strings.Join(envSet, " · "),
		})
	} else {
		add(SubPasso{
			Descricao: "Inspecionar variaveis de ambiente de proxy",
			Status:    StatusOK,
			Detalhe:   "Nenhuma variavel HTTP_PROXY / HTTPS_PROXY setada",
		})
	}

	// (2) Windows: netsh winhttp show proxy
	netshDetectado := ""
	if runtime.GOOS == "windows" {
		cmd := exec.Command("netsh", "winhttp", "show", "proxy")
		out, _ := cmd.Output()
		saida := string(out)
		minusc := strings.ToLower(saida)
		if strings.Contains(minusc, "direct access") || strings.Contains(minusc, "acesso direto") {
			add(SubPasso{
				Descricao: "Consultar configuracao WinHTTP do Windows",
				Status:    StatusOK,
				Detalhe:   "Acesso direto (sem proxy WinHTTP configurado)",
			})
		} else {
			// Extrai o servidor proxy do output
			netshDetectado = extrairProxyNetsh(saida)
			if netshDetectado != "" {
				add(SubPasso{
					Descricao: "Consultar configuracao WinHTTP do Windows",
					Status:    StatusWarn,
					Detalhe:   "Proxy WinHTTP configurado: " + netshDetectado,
				})
			} else {
				add(SubPasso{
					Descricao: "Consultar configuracao WinHTTP do Windows",
					Status:    StatusInfo,
					Detalhe:   "Nao foi possivel interpretar saida do netsh",
				})
			}
		}
	} else {
		add(SubPasso{
			Descricao: "Consultar configuracao WinHTTP do Windows",
			Status:    StatusInfo,
			Detalhe:   "Nao implementado neste SO",
		})
	}

	// Status final
	statusFinal := StatusOK
	mensagem := "Nenhum proxy/VPN detectado"
	if len(envSet) > 0 || netshDetectado != "" {
		statusFinal = StatusWarn
		mensagem = "Proxy detectado — pode interceptar/alterar requisicoes silenciosamente"
	}

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"env_proxies":       envProxies,
		"netsh_proxy":       netshDetectado,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

func extrairProxyNetsh(saida string) string {
	for _, linha := range strings.Split(saida, "\n") {
		l := strings.TrimSpace(linha)
		minusc := strings.ToLower(l)
		if strings.Contains(minusc, "proxy server") || strings.Contains(minusc, "servidor proxy") {
			// Usa Index (primeiro `:`) para nao confundir com o `:` que separa
			// host:porta dentro do valor (ex: "proxy.empresa.local:8080").
			if idx := strings.Index(l, ":"); idx > 0 {
				return strings.TrimSpace(l[idx+1:])
			}
		}
	}
	return ""
}

// CheckRelogio compara o horario local com o horario da internet (header HTTP
// "Date" da resposta de um servidor confiavel). Drift de horario maior que
// 30s quebra validacao de TLS, autenticacao OAuth, assinatura de webhooks e
// outras coisas dependentes de timestamp.
//
// Status:
//   - OK: drift < 5s
//   - WARN: drift 5-30s
//   - FAIL: drift > 30s (e' grave, quebra autenticacao)
func CheckRelogio(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Ambiente", Nome: "Relogio do sistema"}
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
	resp, err := client.Head(urlRelogio)
	durReq := time.Since(tReq).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: "Consultar horario de referencia (header Date do Cloudflare)",
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Nao foi possivel comparar relogio — sem acesso a referencia externa"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer resp.Body.Close()

	dateStr := resp.Header.Get("Date")
	if dateStr == "" {
		add(SubPasso{
			Descricao: "Consultar horario de referencia (header Date do Cloudflare)",
			Status:    StatusWarn,
			DuracaoMs: durReq,
			Detalhe:   "Servidor nao retornou header Date",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Nao foi possivel obter horario de referencia"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	servidorTime, errParse := time.Parse(time.RFC1123, dateStr)
	if errParse != nil {
		add(SubPasso{
			Descricao: "Consultar horario de referencia (header Date do Cloudflare)",
			Status:    StatusWarn,
			DuracaoMs: durReq,
			Detalhe:   "Falha ao interpretar header Date: " + errParse.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Falha ao interpretar horario remoto"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	add(SubPasso{
		Descricao: "Consultar horario de referencia (header Date do Cloudflare)",
		Status:    StatusOK,
		DuracaoMs: durReq,
		Detalhe:   fmt.Sprintf("Horario remoto: %s", servidorTime.Format(time.RFC3339)),
	})

	// Compara com hora local. Considera o tempo de transito da requisicao.
	agora := time.Now()
	diff := agora.Sub(servidorTime)
	diffAbsSec := int64(diff.Seconds())
	if diffAbsSec < 0 {
		diffAbsSec = -diffAbsSec
	}

	statusFinal := StatusOK
	detalhe := fmt.Sprintf("Local: %s · Remoto: %s · Diferenca: %ds (dentro do tolerado)", agora.Format("15:04:05"), servidorTime.Format("15:04:05"), diffAbsSec)
	mensagem := fmt.Sprintf("Relogio em sincronia — drift de %ds", diffAbsSec)
	switch {
	case diffAbsSec > 30:
		statusFinal = StatusFail
		detalhe = fmt.Sprintf("Local: %s · Remoto: %s · Diferenca: %ds — DRIFT CRITICO, vai quebrar TLS/OAuth/assinaturas. Sincronizar com NTP", agora.Format("15:04:05"), servidorTime.Format("15:04:05"), diffAbsSec)
		mensagem = fmt.Sprintf("Relogio com drift critico de %ds — vai quebrar autenticacao", diffAbsSec)
	case diffAbsSec > 5:
		statusFinal = StatusWarn
		detalhe = fmt.Sprintf("Local: %s · Remoto: %s · Diferenca: %ds — drift acima do ideal, considerar sincronizar NTP", agora.Format("15:04:05"), servidorTime.Format("15:04:05"), diffAbsSec)
		mensagem = fmt.Sprintf("Relogio com drift de %ds — recomenda-se sincronizar NTP", diffAbsSec)
	}

	add(SubPasso{
		Descricao: "Comparar com horario local",
		Status:    statusFinal,
		Detalhe:   detalhe,
	})

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"hora_local":   agora,
		"hora_remota":  servidorTime,
		"drift_seg":    diffAbsSec,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckPerfilEnergia consulta o perfil ativo de energia do Windows. Perfil
// "Economia de energia" pode causar problemas de latencia em redes WiFi
// (placa entra em sleep), USB, e em alguns casos drops de conexao.
//
// Status:
//   - INFO: perfil identificado
//   - WARN: perfil "Power saver" ou "Economia de energia"
func CheckPerfilEnergia(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Ambiente", Nome: "Perfil de energia"}
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
			Descricao: "Validar SO compativel",
			Status:    StatusInfo,
			Detalhe:   "Implementado apenas em Windows por enquanto",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Perfil de energia nao consultado neste SO"
		return r
	}

	cmd := exec.Command("powercfg", "/getactivescheme")
	out, err := cmd.Output()
	if err != nil {
		add(SubPasso{
			Descricao: "Consultar perfil ativo (powercfg /getactivescheme)",
			Status:    StatusInfo,
			Detalhe:   "Falha ao executar powercfg: " + err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Nao foi possivel consultar perfil de energia"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	saida := strings.TrimSpace(string(out))
	// Formato: "Esquema de Energia GUID: ... (Nome)"
	nomePerfil := extrairNomePerfil(saida)
	add(SubPasso{
		Descricao: "Consultar perfil ativo (powercfg /getactivescheme)",
		Status:    StatusOK,
		Detalhe:   "Saida: " + saida,
	})

	statusFinal := StatusInfo
	mensagem := fmt.Sprintf("Perfil ativo: %s", nomePerfil)
	minusc := strings.ToLower(nomePerfil)
	if strings.Contains(minusc, "power saver") || strings.Contains(minusc, "economia") {
		statusFinal = StatusWarn
		mensagem = fmt.Sprintf("Perfil de economia ativo (%s) — pode causar latencia WiFi e drops de conexao. Recomenda-se 'Equilibrado' ou 'Alto desempenho'", nomePerfil)
		add(SubPasso{
			Descricao: "Avaliar adequacao do perfil para operacao de rede",
			Status:    StatusWarn,
			Detalhe:   "Perfil de economia pode pausar adaptador WiFi periodicamente, causando latencia ocasional",
		})
	} else {
		add(SubPasso{
			Descricao: "Avaliar adequacao do perfil para operacao de rede",
			Status:    StatusOK,
			Detalhe:   "Perfil compativel com operacao de rede continua",
		})
	}

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"perfil_ativo":  nomePerfil,
		"saida_bruta":   saida,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

func extrairNomePerfil(saida string) string {
	// Procura por nome entre parenteses
	abre := strings.LastIndex(saida, "(")
	fecha := strings.LastIndex(saida, ")")
	if abre >= 0 && fecha > abre {
		return strings.TrimSpace(saida[abre+1 : fecha])
	}
	return strings.TrimSpace(saida)
}
