package checks

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Variaveis de pacote extraidas para permitir testes com httptest. Em produção
// retornam os valores reais (apontam para a infra Vuca/Cloudflare). Em testes
// podem ser sobrescritos para apontar para mock servers locais.
var (
	construtorURLInstancia = func(instancia string) string {
		return fmt.Sprintf("https://%s.vucasolution.com.br/", instancia)
	}
	construtorHostInstancia443 = func(instancia string) string {
		return fmt.Sprintf("%s.vucasolution.com.br:443", instancia)
	}
	dominioRefDNS = "cloudflare.com"
)

// CheckDNS valida a saude do DNS local do cliente (nao a existencia da instancia).
//
// Faz 4 sub-validacoes:
//   1) Resolve um dominio externo conhecido (cloudflare.com) — confirma que o
//      servidor DNS local responde. Se falhar aqui, o DNS local esta quebrado.
//   2) Se instancia foi informada, resolve {instancia}.vucasolution.com.br e
//      detecta se a resolucao caiu via wildcard (zona atras de CDN).
//   3) Mede tempo de resolucao em cada sub-teste — DNS lento e' sintoma classico
//      de roteador/operadora ruim que causa lentidao em aplicacoes.
//   4) Lista os servidores DNS configurados na maquina (so Windows por enquanto;
//      em outros SOs, o campo vem vazio).
//
// Status:
//   - FAIL: dominio externo nao resolveu (DNS local quebrado).
//   - FAIL: resolucao do dominio externo > 2000ms (DNS extremamente lento).
//   - WARN: resolucao > 500ms (DNS lento), ou wildcard detectado.
//   - OK: tudo dentro do esperado.
func CheckDNS(instancia string, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Conectividade", Nome: "Resolucao DNS"}
	detalhes := map[string]interface{}{}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	// (1) Resolucao de dominio externo conhecido — valida o DNS local.
	dominioRef := dominioRefDNS
	tRef := time.Now()
	ipsRef, errRef := net.LookupIP(dominioRef)
	durRef := time.Since(tRef).Milliseconds()

	refMap := map[string]interface{}{
		"host":       dominioRef,
		"duracao_ms": durRef,
	}
	if errRef != nil {
		refMap["erro"] = errRef.Error()
		detalhes["referencia"] = refMap
		detalhes["servidores_dns"] = dnsServersConfigurados()
		add(SubPasso{
			Descricao: fmt.Sprintf("Resolver dominio externo (%s)", dominioRef),
			Status:    StatusFail,
			DuracaoMs: durRef,
			Detalhe:   errRef.Error(),
		})
		r.Detalhes = detalhes
		r.SubPassos = subpassos
		r.DuracaoMs = durRef
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("DNS local quebrado: nao conseguiu resolver %s (%s)", dominioRef, errRef.Error())
		return r
	}
	refMap["ips"] = ipsParaStrings(ipsRef)
	detalhes["referencia"] = refMap
	add(SubPasso{
		Descricao: fmt.Sprintf("Resolver dominio externo (%s)", dominioRef),
		Status:    StatusOK,
		DuracaoMs: durRef,
		Detalhe:   fmt.Sprintf("Resolveu para %v", ipsParaStrings(ipsRef)),
	})

	// (2) Resolucao do dominio da instancia (se informada) + detecao de wildcard.
	var durInst int64
	wildcardDetectado := false
	if instancia != "" {
		host := fmt.Sprintf("%s.vucasolution.com.br", instancia)
		tInst := time.Now()
		ipsInst, errInst := net.LookupIP(host)
		durInst = time.Since(tInst).Milliseconds()

		instMap := map[string]interface{}{
			"host":       host,
			"duracao_ms": durInst,
		}
		if errInst != nil {
			instMap["erro"] = errInst.Error()
			add(SubPasso{
				Descricao: fmt.Sprintf("Resolver dominio da instancia (%s)", host),
				Status:    StatusFail,
				DuracaoMs: durInst,
				Detalhe:   errInst.Error(),
			})
		} else {
			listaIps := ipsParaStrings(ipsInst)
			instMap["ips"] = listaIps
			add(SubPasso{
				Descricao: fmt.Sprintf("Resolver dominio da instancia (%s)", host),
				Status:    StatusOK,
				DuracaoMs: durInst,
				Detalhe:   fmt.Sprintf("Resolveu para %v", listaIps),
			})

			// Detecta wildcard resolvendo um subdominio inventado.
			tWild := time.Now()
			canario := fmt.Sprintf("_diag_invalido_%d_.vucasolution.com.br", time.Now().UnixNano())
			canarioIPs, errC := net.LookupIP(canario)
			durWild := time.Since(tWild).Milliseconds()
			if errC == nil && len(canarioIPs) > 0 {
				canarioList := ipsParaStrings(canarioIPs)
				if intersecaoIPs(listaIps, canarioList) {
					wildcardDetectado = true
					instMap["wildcard_detectado"] = true
					instMap["canario_host"] = canario
					instMap["canario_ips"] = canarioList
					add(SubPasso{
						Descricao: "Detectar wildcard DNS na zona",
						Status:    StatusWarn,
						DuracaoMs: durWild,
						Detalhe:   "Wildcard detectado — DNS sozinho nao confirma a instancia",
					})
				} else {
					instMap["wildcard_detectado"] = false
					add(SubPasso{
						Descricao: "Detectar wildcard DNS na zona",
						Status:    StatusOK,
						DuracaoMs: durWild,
						Detalhe:   "Sem wildcard — DNS confirma a instancia especificamente",
					})
				}
			} else {
				add(SubPasso{
					Descricao: "Detectar wildcard DNS na zona",
					Status:    StatusInfo,
					DuracaoMs: durWild,
					Detalhe:   "Nao foi possivel testar (dominio canario nao resolveu)",
				})
			}

			// (2.1) Comparacao multi-servidor — detecta DNS hijacking ou cache zoado.
			divergencias := []string{}
			tCF := time.Now()
			ipsCF, errCF := resolverViaServidor("1.1.1.1:53", host)
			durCF := time.Since(tCF).Milliseconds()
			if errCF == nil {
				add(SubPasso{
					Descricao: "Resolver instancia via 1.1.1.1 (Cloudflare DNS)",
					Status:    StatusOK,
					DuracaoMs: durCF,
					Detalhe:   fmt.Sprintf("Resolveu para %v", ipsCF),
				})
				if !intersecaoIPs(listaIps, ipsCF) {
					divergencias = append(divergencias, "1.1.1.1")
				}
			} else {
				add(SubPasso{
					Descricao: "Resolver instancia via 1.1.1.1 (Cloudflare DNS)",
					Status:    StatusWarn,
					DuracaoMs: durCF,
					Detalhe:   errCF.Error(),
				})
			}
			tGG := time.Now()
			ipsGG, errGG := resolverViaServidor("8.8.8.8:53", host)
			durGG := time.Since(tGG).Milliseconds()
			if errGG == nil {
				add(SubPasso{
					Descricao: "Resolver instancia via 8.8.8.8 (Google DNS)",
					Status:    StatusOK,
					DuracaoMs: durGG,
					Detalhe:   fmt.Sprintf("Resolveu para %v", ipsGG),
				})
				if !intersecaoIPs(listaIps, ipsGG) {
					divergencias = append(divergencias, "8.8.8.8")
				}
			} else {
				add(SubPasso{
					Descricao: "Resolver instancia via 8.8.8.8 (Google DNS)",
					Status:    StatusWarn,
					DuracaoMs: durGG,
					Detalhe:   errGG.Error(),
				})
			}
			if errCF == nil && errGG == nil {
				if len(divergencias) == 0 {
					add(SubPasso{
						Descricao: "Comparar respostas dos servidores DNS",
						Status:    StatusOK,
						Detalhe:   "DNS local, 1.1.1.1 e 8.8.8.8 resolveram convergente — sem sinal de hijacking",
					})
				} else {
					instMap["dns_divergente"] = divergencias
					add(SubPasso{
						Descricao: "Comparar respostas dos servidores DNS",
						Status:    StatusWarn,
						Detalhe:   fmt.Sprintf("DNS local resolveu DIFERENTE de %v — possivel cache zoado, DNS de operadora alterando respostas, ou hijacking", divergencias),
					})
				}
			}

			// (2.2) IPv4 vs IPv6 separados — detecta AAAA broken (causa de "tablet 10s mais lento").
			ctxIP, cancelIP := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelIP()
			tV4 := time.Now()
			addrsV4, errV4 := net.DefaultResolver.LookupIP(ctxIP, "ip4", host)
			durV4 := time.Since(tV4).Milliseconds()
			if errV4 == nil {
				add(SubPasso{
					Descricao: "Resolver registros A (IPv4)",
					Status:    StatusOK,
					DuracaoMs: durV4,
					Detalhe:   fmt.Sprintf("Resolveu para %v", ipsParaStrings(addrsV4)),
				})
			} else {
				add(SubPasso{
					Descricao: "Resolver registros A (IPv4)",
					Status:    StatusWarn,
					DuracaoMs: durV4,
					Detalhe:   errV4.Error(),
				})
			}
			tV6 := time.Now()
			addrsV6, errV6 := net.DefaultResolver.LookupIP(ctxIP, "ip6", host)
			durV6 := time.Since(tV6).Milliseconds()
			if errV6 != nil || len(addrsV6) == 0 {
				add(SubPasso{
					Descricao: "Resolver registros AAAA (IPv6)",
					Status:    StatusInfo,
					DuracaoMs: durV6,
					Detalhe:   "Sem AAAA registrado — esperado para dominios sem IPv6 (sem impacto)",
				})
			} else {
				ipv6Lista := ipsParaStrings(addrsV6)
				instMap["ipv6"] = ipv6Lista
				add(SubPasso{
					Descricao: "Resolver registros AAAA (IPv6)",
					Status:    StatusOK,
					DuracaoMs: durV6,
					Detalhe:   fmt.Sprintf("Resolveu para %v", ipv6Lista),
				})
				// Testa conectividade no IPv6 (porta 443)
				tConV6 := time.Now()
				endIPv6 := fmt.Sprintf("[%s]:443", addrsV6[0].String())
				connV6, errConV6 := net.DialTimeout("tcp", endIPv6, 3*time.Second)
				durConV6 := time.Since(tConV6).Milliseconds()
				if errConV6 == nil {
					connV6.Close()
					add(SubPasso{
						Descricao: "Conectividade TCP/443 via IPv6",
						Status:    StatusOK,
						DuracaoMs: durConV6,
						Detalhe:   fmt.Sprintf("Conectou em %s", endIPv6),
					})
				} else {
					instMap["ipv6_quebrado"] = true
					add(SubPasso{
						Descricao: "Conectividade TCP/443 via IPv6",
						Status:    StatusWarn,
						DuracaoMs: durConV6,
						Detalhe:   fmt.Sprintf("AAAA registrado mas IPv6 nao roteia (%s) — pode causar latencia de timeout (~10s) na primeira tentativa de aplicacoes", normalizaErroRede(errConV6)),
					})
				}
			}

			// (2.3) Estabilidade — 3 lookups com 200ms de delay.
			estavel := true
			primeiraResolucao := listaIps
			for i := 0; i < 3; i++ {
				time.Sleep(200 * time.Millisecond)
				ipsRep, errRep := net.LookupIP(host)
				if errRep != nil {
					estavel = false
					continue
				}
				if !mesmoSetIPs(primeiraResolucao, ipsParaStrings(ipsRep)) {
					estavel = false
				}
			}
			if estavel {
				add(SubPasso{
					Descricao: "Validar estabilidade DNS (3 resolucoes seguidas)",
					Status:    StatusOK,
					Detalhe:   "DNS estavel — 3 lookups com mesmo resultado",
				})
			} else {
				instMap["dns_instavel"] = true
				add(SubPasso{
					Descricao: "Validar estabilidade DNS (3 resolucoes seguidas)",
					Status:    StatusWarn,
					Detalhe:   "DNS instavel — resolucoes consecutivas trouxeram resultados diferentes ou falharam intermitentemente",
				})
			}
		}
		detalhes["instancia"] = instMap
	} else {
		add(SubPasso{
			Descricao: "Resolver dominio da instancia",
			Status:    StatusInfo,
			Detalhe:   "Instancia nao informada — sub-validacao pulada",
		})
	}

	// (4) Servidores DNS configurados na maquina.
	servidores := dnsServersConfigurados()
	detalhes["servidores_dns"] = servidores
	if runtime.GOOS == "windows" {
		detSrv := ""
		if len(servidores) > 0 {
			detSrv = fmt.Sprintf("Servidores: %v", servidores)
		} else {
			detSrv = "Nao foi possivel extrair (ipconfig falhou ou sem entradas)"
		}
		add(SubPasso{
			Descricao: "Listar servidores DNS configurados no SO",
			Status:    StatusInfo,
			Detalhe:   detSrv,
		})
	} else {
		add(SubPasso{
			Descricao: "Listar servidores DNS configurados no SO",
			Status:    StatusInfo,
			Detalhe:   "Nao implementado neste SO",
		})
	}

	r.Detalhes = detalhes
	r.SubPassos = subpassos
	r.DuracaoMs = durRef + durInst

	// (3) Avaliacao final baseada em tempos e wildcard.
	switch {
	case durRef > 2000:
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("DNS local extremamente lento (%dms para resolver %s) — pode causar travamentos em aplicacoes", durRef, dominioRef)
		return r
	}

	avisos := []string{}
	if durRef > 500 {
		avisos = append(avisos, fmt.Sprintf("DNS de referencia lento (%dms para %s)", durRef, dominioRef))
	}
	if instancia != "" && durInst > 500 {
		avisos = append(avisos, fmt.Sprintf("DNS da instancia lento (%dms)", durInst))
	}
	if wildcardDetectado {
		avisos = append(avisos, "instancia resolveu via wildcard DNS — DNS sozinho nao confirma a instancia (HTTPS confirma)")
	}

	if len(avisos) > 0 {
		r.Status = StatusWarn
		r.Mensagem = "Aviso: " + strings.Join(avisos, "; ")
	} else {
		r.Status = StatusOK
		if instancia != "" {
			r.Mensagem = fmt.Sprintf("DNS local saudavel — referencia %dms, instancia %dms", durRef, durInst)
		} else {
			r.Mensagem = fmt.Sprintf("DNS local saudavel — referencia %dms", durRef)
		}
	}
	return r
}

func ipsParaStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

// resolverViaServidor resolve um host usando um servidor DNS especifico
// (formato "ip:porta"). Util para comparar a resolucao do DNS local com
// servidores publicos e detectar DNS hijacking / cache zoado / DNS de
// operadora alterando respostas.
func resolverViaServidor(servidor, host string) ([]string, error) {
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "udp", servidor)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ips, err := r.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	return ips, nil
}

// mesmoSetIPs compara dois conjuntos de IPs (independente da ordem).
// Usado para detectar instabilidade DNS (resolucoes consecutivas que retornam
// resultados diferentes — sintoma de cache mal configurado ou round-robin
// inconsistente entre servidores DNS).
func mesmoSetIPs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		if !set[v] {
			return false
		}
	}
	return true
}

// dnsServersConfigurados retorna os servidores DNS configurados na maquina.
// So implementado para Windows (parsing de `ipconfig /all`). Em outros SOs,
// retorna lista vazia (campo fica vazio no relatorio).
func dnsServersConfigurados() []string {
	if runtime.GOOS != "windows" {
		return []string{}
	}
	cmd := exec.Command("ipconfig", "/all")
	out, err := cmd.Output()
	if err != nil {
		return []string{}
	}
	return parseDNSServersIpconfig(string(out))
}

var reIPLiteral = regexp.MustCompile(`^[0-9a-fA-F\.:%]+$`)

// parseDNSServersIpconfig extrai IPs de servidores DNS do output do `ipconfig /all`
// no Windows. Procura por linhas tipo "Servidores DNS . . . . . . . . . . . : 192.168.0.1"
// e continua coletando IPs nas linhas subsequentes indentadas ate' aparecer outra secao.
func parseDNSServersIpconfig(saida string) []string {
	saida = strings.ReplaceAll(saida, "\r", "")
	linhas := strings.Split(saida, "\n")
	servidores := []string{}
	dentroSecaoDNS := false
	for _, linha := range linhas {
		l := strings.TrimSpace(linha)
		minusc := strings.ToLower(l)
		if strings.Contains(minusc, "dns server") || strings.Contains(minusc, "servidores dns") || strings.Contains(minusc, "servidor dns") {
			dentroSecaoDNS = true
			if idx := strings.LastIndex(l, ":"); idx > 0 {
				candidato := strings.TrimSpace(l[idx+1:])
				if reIPLiteral.MatchString(candidato) {
					servidores = append(servidores, candidato)
				}
			}
			continue
		}
		if dentroSecaoDNS {
			if l == "" {
				continue
			}
			if reIPLiteral.MatchString(l) {
				servidores = append(servidores, l)
				continue
			}
			dentroSecaoDNS = false
		}
	}
	return removerDuplicatas(servidores)
}

func removerDuplicatas(in []string) []string {
	vistos := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if !vistos[v] {
			vistos[v] = true
			out = append(out, v)
		}
	}
	return out
}

func intersecaoIPs(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]bool, len(a))
	for _, v := range a {
		set[v] = true
	}
	for _, v := range b {
		if set[v] {
			return true
		}
	}
	return false
}

// CheckHTTPS faz uma requisicao HEAD para o dominio da instancia.
func CheckHTTPS(instancia string, emit func(SubPasso)) Resultado {
	inicio := time.Now()
	r := Resultado{Categoria: "Conectividade", Nome: "HTTPS"}
	url := construtorURLInstancia(instancia)
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
		},
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "vuca-infra-diagnostico/1.0")
	tReq := time.Now()
	resp, err := client.Do(req)
	durReq := time.Since(tReq).Milliseconds()
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Falha ao acessar %s: %s", url, err.Error())
		add(SubPasso{
			Descricao: fmt.Sprintf("Requisitar GET em %s", url),
			Status:    StatusFail,
			DuracaoMs: durReq,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		return r
	}
	defer resp.Body.Close()
	add(SubPasso{
		Descricao: fmt.Sprintf("Requisitar GET em %s", url),
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

	r.Detalhes = map[string]interface{}{
		"url":          url,
		"status_code":  resp.StatusCode,
		"body_snippet": snippet,
	}

	var detClassif string
	var statusClassif Status
	switch {
	case resp.StatusCode == 404 && strings.Contains(strings.ToLower(body), "default backend"):
		r.Status = StatusFail
		r.Mensagem = "Instancia nao existe no cluster (default backend respondeu 404) — verifique o nome da instancia"
		statusClassif = StatusFail
		detClassif = "404 com body 'default backend' — instancia inexistente"
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Resposta inesperada %d — verifique se a instancia esta correta", resp.StatusCode)
		statusClassif = StatusWarn
		detClassif = fmt.Sprintf("4xx inesperado (%d)", resp.StatusCode)
	case resp.StatusCode >= 500:
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Servidor respondeu %d", resp.StatusCode)
		statusClassif = StatusWarn
		detClassif = fmt.Sprintf("5xx — erro do servidor (%d)", resp.StatusCode)
	default:
		r.Status = StatusOK
		r.Mensagem = fmt.Sprintf("Resposta %d em %dms", resp.StatusCode, r.DuracaoMs)
		statusClassif = StatusOK
		detClassif = fmt.Sprintf("Status %d — instancia respondeu", resp.StatusCode)
	}
	add(SubPasso{
		Descricao: "Classificar status code",
		Status:    statusClassif,
		Detalhe:   detClassif,
	})
	r.SubPassos = subpassos
	return r
}

// CheckHTTPSFases mede separadamente as fases da requisicao HTTPS (DNS, TCP,
// TLS handshake, TTFB) para o dominio da instancia. Util para diagnosticar
// "porque o tablet do cliente abre lento" — diz exatamente em qual etapa esta
// o gargalo (ex: TLS handshake de 8s indica problema de MTU/firewall;
// DNS de 12s indica servidor DNS local ruim).
//
// Thresholds (em ms):
//   - DNS:  500 (WARN) / 2000 (FAIL)
//   - TCP:  500 (WARN) / 3000 (FAIL)
//   - TLS:  1000 (WARN) / 5000 (FAIL)
//   - TTFB: 2000 (WARN) / 10000 (FAIL)
//   - Total: timeout em 20s
func CheckHTTPSFases(instancia string, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Conectividade", Nome: "Tempos de conexao (HTTPS por fase)"}
	if instancia == "" {
		r.Status = StatusInfo
		r.Mensagem = "Instancia nao informada — pulando"
		return r
	}
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}
	url := construtorURLInstancia(instancia)

	var dnsStart, dnsDone time.Time
	var connStart, connDone time.Time
	var tlsStart, tlsDone time.Time
	var primeiroByte time.Time

	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectStart:         func(_, _ string) { connStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { connDone = time.Now() },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { tlsDone = time.Now() },
		GotFirstResponseByte: func() { primeiroByte = time.Now() },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", url, nil)
	req.Header.Set("User-Agent", "vuca-infra-diagnostico/1.0")

	// DisableKeepAlives garante que dns/connect/tls sao medidos numa conexao nova.
	transporte := &http.Transport{
		DisableKeepAlives: true,
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: false},
	}
	client := &http.Client{Timeout: 20 * time.Second, Transport: transporte}

	inicio := time.Now()
	resp, err := client.Do(req)
	totalMs := time.Since(inicio).Milliseconds()
	r.DuracaoMs = totalMs

	if err != nil {
		r.Status = StatusFail
		r.Detalhes = map[string]interface{}{
			"url":      url,
			"total_ms": totalMs,
			"erro":     err.Error(),
			"dns_ms":   diffMs(dnsStart, dnsDone),
			"tcp_ms":   diffMs(connStart, connDone),
			"tls_ms":   diffMs(tlsStart, tlsDone),
		}
		if errors_isTimeout(err) {
			r.Mensagem = fmt.Sprintf("Conexao excedeu 20s — o cliente nao consegue abrir a aplicacao em tempo aceitavel. Isso explica \"tablet abre devagar/travado\"")
		} else {
			r.Mensagem = fmt.Sprintf("Falha durante requisicao em %dms: %s", totalMs, err.Error())
		}
		return r
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	dnsMs := diffMs(dnsStart, dnsDone)
	tcpMs := diffMs(connStart, connDone)
	tlsMs := diffMs(tlsStart, tlsDone)
	ttfbMs := diffMs(inicio, primeiroByte)

	r.Detalhes = map[string]interface{}{
		"url":         url,
		"dns_ms":      dnsMs,
		"tcp_ms":      tcpMs,
		"tls_ms":      tlsMs,
		"ttfb_ms":     ttfbMs,
		"total_ms":    totalMs,
		"status_code": resp.StatusCode,
	}

	avalFase := func(ms, limWarn, limFail int64) (Status, string) {
		switch {
		case ms >= limFail:
			return StatusFail, fmt.Sprintf("%dms — critico (>= %dms)", ms, limFail)
		case ms >= limWarn:
			return StatusWarn, fmt.Sprintf("%dms — lento (>= %dms)", ms, limWarn)
		default:
			return StatusOK, fmt.Sprintf("%dms", ms)
		}
	}

	stDNS, detDNS := avalFase(dnsMs, 500, 2000)
	stTCP, detTCP := avalFase(tcpMs, 500, 3000)
	stTLS, detTLS := avalFase(tlsMs, 1000, 5000)
	stTTFB, detTTFB := avalFase(ttfbMs, 2000, 10000)

	add(SubPasso{Descricao: "Fase DNS — resolucao de nome", Status: stDNS, DuracaoMs: dnsMs, Detalhe: detDNS})
	add(SubPasso{Descricao: "Fase TCP — conexao na porta 443", Status: stTCP, DuracaoMs: tcpMs, Detalhe: detTCP})
	add(SubPasso{Descricao: "Fase TLS — handshake criptografico", Status: stTLS, DuracaoMs: tlsMs, Detalhe: detTLS})
	add(SubPasso{Descricao: "Fase TTFB — primeiro byte de resposta", Status: stTTFB, DuracaoMs: ttfbMs, Detalhe: detTTFB})
	r.SubPassos = subpassos

	falhas := []string{}
	avisos := []string{}
	if dnsMs >= 2000 {
		falhas = append(falhas, fmt.Sprintf("DNS critico (%dms)", dnsMs))
	} else if dnsMs >= 500 {
		avisos = append(avisos, fmt.Sprintf("DNS lento (%dms)", dnsMs))
	}
	if tcpMs >= 3000 {
		falhas = append(falhas, fmt.Sprintf("TCP critico (%dms)", tcpMs))
	} else if tcpMs >= 500 {
		avisos = append(avisos, fmt.Sprintf("TCP lento (%dms)", tcpMs))
	}
	if tlsMs >= 5000 {
		falhas = append(falhas, fmt.Sprintf("TLS critico (%dms)", tlsMs))
	} else if tlsMs >= 1000 {
		avisos = append(avisos, fmt.Sprintf("TLS lento (%dms)", tlsMs))
	}
	if ttfbMs >= 10000 {
		falhas = append(falhas, fmt.Sprintf("TTFB critico (%dms)", ttfbMs))
	} else if ttfbMs >= 2000 {
		avisos = append(avisos, fmt.Sprintf("TTFB lento (%dms)", ttfbMs))
	}

	resumoFases := fmt.Sprintf("DNS %dms / TCP %dms / TLS %dms / TTFB %dms / total %dms", dnsMs, tcpMs, tlsMs, ttfbMs, totalMs)

	switch {
	case len(falhas) > 0:
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Lentidao critica em: %s. Aplicacoes vao travar/timeout neste cliente. (%s)", strings.Join(falhas, ", "), resumoFases)
	case len(avisos) > 0:
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Lentidao detectada em: %s. Aplicacoes podem abrir devagar neste cliente. (%s)", strings.Join(avisos, ", "), resumoFases)
	default:
		r.Status = StatusOK
		r.Mensagem = "Conexao saudavel — " + resumoFases
	}
	return r
}

func diffMs(inicio, fim time.Time) int64 {
	if inicio.IsZero() || fim.IsZero() {
		return 0
	}
	return fim.Sub(inicio).Milliseconds()
}

// errors_isTimeout faz uma checagem simples de timeout sem importar "errors".
// Usa a string do erro para nao ter que arrastar net.Error type assertion.
func errors_isTimeout(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "Client.Timeout") || strings.Contains(msg, "i/o timeout")
}

// CheckHTTPSConsistencia faz 3 requisicoes HTTPS sequenciais (com 1s de
// intervalo) e compara as respostas para detectar:
//   - Variabilidade alta de tempo (sintoma de balanceador inconsistente)
//   - Variabilidade de status code entre tentativas (sintoma de instabilidade
//     atras do balanceador)
//   - 200 OK enganoso (status 200 com body indicando erro — caso classico de
//     API que sempre retorna 200 mas com {"error": ...})
//
// Status:
//   - FAIL: 1+ requisicao falhou completamente
//   - WARN: status codes diferentes entre as 3 ou body com indicio de erro
//   - WARN: variacao de tempo > 3x entre o mais rapido e o mais lento
//   - OK: 3 respostas iguais e consistentes
func CheckHTTPSConsistencia(instancia string, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Conectividade", Nome: "Consistencia HTTPS (3 requisicoes)"}
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

	url := construtorURLInstancia(instancia)
	inicio := time.Now()

	type amostra struct {
		statusCode int
		tempoMs    int64
		bodyHash   string
		erro       string
	}
	amostras := make([]amostra, 0, 3)

	for i := 0; i < 3; i++ {
		client := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DisableKeepAlives: true,
				TLSClientConfig:   &tls.Config{InsecureSkipVerify: false},
			},
		}
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "vuca-infra-diagnostico/1.0")
		t0 := time.Now()
		resp, err := client.Do(req)
		dur := time.Since(t0).Milliseconds()

		am := amostra{tempoMs: dur}
		if err != nil {
			am.erro = err.Error()
			add(SubPasso{
				Descricao: fmt.Sprintf("Requisicao %d/3 em %s", i+1, url),
				Status:    StatusFail,
				DuracaoMs: dur,
				Detalhe:   err.Error(),
			})
		} else {
			am.statusCode = resp.StatusCode
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			resp.Body.Close()
			// Hash simples do body (primeiros 128 bytes) para detectar variacao
			snippet := strings.ToLower(string(body))
			if len(snippet) > 128 {
				am.bodyHash = snippet[:128]
			} else {
				am.bodyHash = snippet
			}
			add(SubPasso{
				Descricao: fmt.Sprintf("Requisicao %d/3 em %s", i+1, url),
				Status:    StatusOK,
				DuracaoMs: dur,
				Detalhe:   fmt.Sprintf("HTTP %d em %dms", resp.StatusCode, dur),
			})
		}
		amostras = append(amostras, am)
		if i < 2 {
			time.Sleep(1 * time.Second)
		}
	}

	// Caso especial: TODAS as 3 falharam por erro de rede.
	// Sem isso a analise abaixo classificaria como "consistente" (todas
	// retornaram status 0, "consistent").
	todasFalharam := true
	for _, am := range amostras {
		if am.erro == "" {
			todasFalharam = false
			break
		}
	}
	if todasFalharam {
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Todas as 3 requisicoes falharam por erro de rede: %s", amostras[0].erro)
		r.Detalhes = map[string]interface{}{
			"url":         url,
			"erro_comum":  amostras[0].erro,
			"todas_falharam": true,
		}
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}

	// Analise: status codes iguais? Tempos consistentes? Body com erro?
	statusVariavel := false
	statusBase := amostras[0].statusCode
	for _, am := range amostras[1:] {
		if am.statusCode != statusBase {
			statusVariavel = true
			break
		}
	}

	tempos := []int64{amostras[0].tempoMs, amostras[1].tempoMs, amostras[2].tempoMs}
	var minTempo, maxTempo int64 = tempos[0], tempos[0]
	for _, t := range tempos[1:] {
		if t < minTempo {
			minTempo = t
		}
		if t > maxTempo {
			maxTempo = t
		}
	}
	temposInstaveis := maxTempo > 3*minTempo && maxTempo-minTempo > 500

	// Detecta 200-com-erro: status 2xx mas body parece ser JSON com erro
	// embutido. So' procura por padroes de chave JSON ("error":, "errors":,
	// "errorMessage":, "errorCode":) para evitar falso positivo em paginas
	// HTML legitimas que contenham palavras tipo "exception", "error-handler",
	// "internal server", etc. em scripts/meta tags.
	indiciosErro := []string{
		`"error":`, `"errors":`, `"errormessage":`, `"errorcode":`,
		`"erro":`, `"erros":`, `"mensagemerro":`,
		`"failed":`, `"falhou":`,
	}
	body200ComErro := false
	for _, am := range amostras {
		if am.statusCode >= 200 && am.statusCode < 300 {
			// So' considera "body parece JSON" se comeca com { ou [ (apos trim)
			bodyTrim := strings.TrimLeft(am.bodyHash, " \t\n\r")
			if !strings.HasPrefix(bodyTrim, "{") && !strings.HasPrefix(bodyTrim, "[") {
				continue
			}
			for _, indicio := range indiciosErro {
				if strings.Contains(am.bodyHash, indicio) {
					body200ComErro = true
					break
				}
			}
		}
	}

	if statusVariavel {
		add(SubPasso{
			Descricao: "Comparar status code entre as 3 requisicoes",
			Status:    StatusWarn,
			Detalhe:   fmt.Sprintf("Status codes variaram: %d, %d, %d — possivel instabilidade atras do balanceador", amostras[0].statusCode, amostras[1].statusCode, amostras[2].statusCode),
		})
	} else {
		add(SubPasso{
			Descricao: "Comparar status code entre as 3 requisicoes",
			Status:    StatusOK,
			Detalhe:   fmt.Sprintf("3 requisicoes retornaram %d (consistente)", statusBase),
		})
	}

	if temposInstaveis {
		add(SubPasso{
			Descricao: "Comparar tempos de resposta",
			Status:    StatusWarn,
			Detalhe:   fmt.Sprintf("Variacao grande: min %dms, max %dms (>3x) — backend inconsistente, balanceador com no fora ou cold start", minTempo, maxTempo),
		})
	} else {
		add(SubPasso{
			Descricao: "Comparar tempos de resposta",
			Status:    StatusOK,
			Detalhe:   fmt.Sprintf("Tempos consistentes: %dms, %dms, %dms", tempos[0], tempos[1], tempos[2]),
		})
	}

	if body200ComErro {
		add(SubPasso{
			Descricao: "Inspecionar body em busca de indicio de erro semantico",
			Status:    StatusWarn,
			Detalhe:   "Body de resposta 2xx contem palavras-chave de erro (error/exception/failed) — pode ser API que retorna 200 com erro embutido",
		})
	} else {
		add(SubPasso{
			Descricao: "Inspecionar body em busca de indicio de erro semantico",
			Status:    StatusOK,
			Detalhe:   "Body limpo, sem indicios de erro embutido em resposta 2xx",
		})
	}

	// Status final
	statusFinal := StatusOK
	avisos := []string{}
	if statusVariavel {
		avisos = append(avisos, "status codes variam entre requisicoes")
	}
	if temposInstaveis {
		avisos = append(avisos, "tempos de resposta muito variaveis")
	}
	if body200ComErro {
		avisos = append(avisos, "resposta 2xx contem indicios de erro")
	}
	mensagem := fmt.Sprintf("3 requisicoes consistentes — todas HTTP %d, tempos %dms-%dms", statusBase, minTempo, maxTempo)
	if len(avisos) > 0 {
		statusFinal = StatusWarn
		mensagem = "Inconsistencia detectada: " + strings.Join(avisos, ", ")
	}

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"url":            url,
		"tempos_ms":      tempos,
		"status_codes":   []int{amostras[0].statusCode, amostras[1].statusCode, amostras[2].statusCode},
		"min_ms":         minTempo,
		"max_ms":         maxTempo,
		"status_variavel": statusVariavel,
		"tempos_instaveis": temposInstaveis,
		"body_200_com_erro": body200ComErro,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// CheckLatencia mede latencia TCP fazendo N conexoes ao host:443.
func CheckLatencia(instancia string, amostras int, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Conectividade", Nome: "Latencia / Perda de pacote"}
	host := construtorHostInstancia443(instancia)

	if amostras <= 0 {
		amostras = 10
	}
	var sucessos int
	var totalMs int64
	var maxMs int64
	var minMs int64 = -1
	subpassos := make([]SubPasso, 0, amostras)
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	inicio := time.Now()
	for i := 0; i < amostras; i++ {
		t0 := time.Now()
		conn, err := net.DialTimeout("tcp", host, 3*time.Second)
		dur := time.Since(t0).Milliseconds()
		descricao := fmt.Sprintf("Amostra %d/%d — TCP em %s", i+1, amostras, host)
		if err == nil {
			conn.Close()
			sucessos++
			totalMs += dur
			if dur > maxMs {
				maxMs = dur
			}
			if minMs < 0 || dur < minMs {
				minMs = dur
			}
			add(SubPasso{
				Descricao: descricao,
				Status:    StatusOK,
				DuracaoMs: dur,
				Detalhe:   "Conectou com sucesso",
			})
		} else {
			add(SubPasso{
				Descricao: descricao,
				Status:    StatusFail,
				DuracaoMs: dur,
				Detalhe:   normalizaErroRede(err),
			})
		}
	}
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	r.SubPassos = subpassos

	perda := float64(amostras-sucessos) / float64(amostras) * 100
	var mediaMs int64
	if sucessos > 0 {
		mediaMs = totalMs / int64(sucessos)
	}

	r.Detalhes = map[string]interface{}{
		"host":     host,
		"amostras": amostras,
		"sucessos": sucessos,
		"perda_pct": perda,
		"media_ms": mediaMs,
		"min_ms":   minMs,
		"max_ms":   maxMs,
	}

	switch {
	case perda >= 50:
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Perda de pacote critica: %.0f%% (%d/%d falharam)", perda, amostras-sucessos, amostras)
	case perda > 0:
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Perda de pacote: %.0f%% — latencia media %dms (min %d, max %d)", perda, mediaMs, minMs, maxMs)
	case mediaMs > 500:
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Latencia alta: media %dms (min %d, max %d)", mediaMs, minMs, maxMs)
	default:
		r.Status = StatusOK
		r.Mensagem = fmt.Sprintf("Sem perda de pacote — latencia media %dms (min %d, max %d)", mediaMs, minMs, maxMs)
	}
	return r
}
