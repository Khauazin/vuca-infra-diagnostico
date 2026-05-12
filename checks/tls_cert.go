package checks

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strings"
	"time"
)

// CheckTLS valida o certificado HTTPS da instancia: expiracao, versao TLS,
// cipher suite, cadeia completa e — importante — detecta se algum antivirus
// local esta interceptando a conexao (TLS MITM por software de seguranca
// instalado na maquina do cliente).
//
// Status:
//   - FAIL: certificado expirado ou expira em < 7 dias
//   - FAIL: TLS < 1.2
//   - WARN: certificado expira em < 30 dias
//   - WARN: cadeia de cert local injetada por antivirus (Bitdefender, Kaspersky, etc)
//   - OK: tudo dentro do esperado
func CheckTLS(instancia string, emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Conectividade", Nome: "Certificado TLS / SSL"}
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
			Detalhe:   "Instancia nao informada — pulando validacao de certificado",
		})
		r.SubPassos = subpassos
		r.Status = StatusInfo
		r.Mensagem = "Instancia nao informada — pulando"
		return r
	}

	inicio := time.Now()
	host := fmt.Sprintf("%s.vucasolution.com.br", instancia)
	endereco := fmt.Sprintf("%s:443", host)

	// Conecta com TLS aceitando o erro de validacao para depois inspecionar
	// detalhes mesmo se cadeia estiver quebrada.
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tTLS := time.Now()
	conn, err := tls.DialWithDialer(dialer, "tcp", endereco, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: false,
	})
	durTLS := time.Since(tTLS).Milliseconds()
	if err != nil {
		add(SubPasso{
			Descricao: fmt.Sprintf("Estabelecer conexao TLS em %s", endereco),
			Status:    StatusFail,
			DuracaoMs: durTLS,
			Detalhe:   err.Error(),
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Falha na conexao TLS: " + err.Error()
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	defer conn.Close()
	add(SubPasso{
		Descricao: fmt.Sprintf("Estabelecer conexao TLS em %s", endereco),
		Status:    StatusOK,
		DuracaoMs: durTLS,
		Detalhe:   "Handshake TLS completo",
	})

	state := conn.ConnectionState()

	// (1) Versao TLS negociada
	versao, versaoStatus, versaoDet := descreverVersaoTLS(state.Version)
	add(SubPasso{
		Descricao: "Identificar versao TLS negociada",
		Status:    versaoStatus,
		Detalhe:   versaoDet,
	})

	// (2) Cipher suite
	cipherNome := tls.CipherSuiteName(state.CipherSuite)
	add(SubPasso{
		Descricao: "Identificar cipher suite negociada",
		Status:    StatusInfo,
		Detalhe:   cipherNome,
	})

	// (3) Validade do certificado
	if len(state.PeerCertificates) == 0 {
		add(SubPasso{
			Descricao: "Inspecionar certificado",
			Status:    StatusFail,
			Detalhe:   "Servidor nao apresentou certificado",
		})
		r.SubPassos = subpassos
		r.Status = StatusFail
		r.Mensagem = "Sem certificado apresentado pelo servidor"
		r.DuracaoMs = time.Since(inicio).Milliseconds()
		return r
	}
	cert := state.PeerCertificates[0]
	expStatus, expDetalhe, diasParaExpirar := classificarValidadeCert(cert.NotAfter, time.Now())
	add(SubPasso{
		Descricao: "Verificar validade do certificado",
		Status:    expStatus,
		Detalhe:   expDetalhe,
	})

	// (4) Cadeia de certificados
	chainLen := len(state.PeerCertificates)
	add(SubPasso{
		Descricao: "Inspecionar cadeia de certificados",
		Status:    StatusInfo,
		Detalhe:   fmt.Sprintf("%d certificado(s) na cadeia (folha + intermediarios)", chainLen),
	})

	// (5) Detectar antivirus interceptando TLS — inspeciona o issuer da cadeia.
	avDetectado := detectarAntivirusMITM(state.PeerCertificates)
	if avDetectado != "" {
		add(SubPasso{
			Descricao: "Detectar interceptacao TLS por antivirus local",
			Status:    StatusWarn,
			Detalhe:   fmt.Sprintf("Cadeia inclui CA de %s — o antivirus esta inspecionando trafego HTTPS. Isso pode quebrar algumas integracoes ou aumentar latencia.", avDetectado),
		})
	} else {
		add(SubPasso{
			Descricao: "Detectar interceptacao TLS por antivirus local",
			Status:    StatusOK,
			Detalhe:   "Cadeia limpa — sem sinal de antivirus interceptando TLS",
		})
	}

	// Status final
	statusFinal := StatusOK
	avisos := []string{}
	falhas := []string{}
	if expStatus == StatusFail {
		falhas = append(falhas, "certificado vencido ou prestes a vencer")
	}
	if versaoStatus == StatusFail {
		falhas = append(falhas, fmt.Sprintf("versao TLS %s obsoleta", versao))
	}
	if expStatus == StatusWarn {
		avisos = append(avisos, fmt.Sprintf("certificado expira em %d dias", diasParaExpirar))
	}
	if avDetectado != "" {
		avisos = append(avisos, fmt.Sprintf("antivirus %s interceptando TLS", avDetectado))
	}
	mensagem := fmt.Sprintf("TLS OK — %s, %s, expira em %d dias", versao, cipherNome, diasParaExpirar)
	switch {
	case len(falhas) > 0:
		statusFinal = StatusFail
		mensagem = "TLS com problemas: " + strings.Join(falhas, ", ")
	case len(avisos) > 0:
		statusFinal = StatusWarn
		mensagem = "TLS com avisos: " + strings.Join(avisos, ", ")
	}

	r.SubPassos = subpassos
	r.Detalhes = map[string]interface{}{
		"host":              host,
		"versao_tls":        versao,
		"cipher_suite":      cipherNome,
		"cert_subject":      cert.Subject.CommonName,
		"cert_issuer":       cert.Issuer.CommonName,
		"cert_validade":    map[string]interface{}{"de": cert.NotBefore, "ate": cert.NotAfter, "dias_restantes": diasParaExpirar},
		"cadeia_tamanho":    chainLen,
		"antivirus_mitm":   avDetectado,
	}
	r.Status = statusFinal
	r.Mensagem = mensagem
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	return r
}

// classificarValidadeCert avalia uma data de expiracao do certificado
// considerando um "agora" de referencia (parametrizavel para testes).
// Retorna o status, uma descricao legivel e o numero de dias ate a expiracao
// (negativo se ja' expirado).
func classificarValidadeCert(naoApos time.Time, agora time.Time) (Status, string, int) {
	diasParaExpirar := int(naoApos.Sub(agora).Hours() / 24)
	switch {
	case diasParaExpirar < 0:
		return StatusFail, fmt.Sprintf("EXPIRADO ha %d dias (em %s)", -diasParaExpirar, naoApos.Format("02/01/2006")), diasParaExpirar
	case diasParaExpirar < 7:
		return StatusFail, fmt.Sprintf("Expira em %d dias (%s) — RENOVAR URGENTE", diasParaExpirar, naoApos.Format("02/01/2006")), diasParaExpirar
	case diasParaExpirar < 30:
		return StatusWarn, fmt.Sprintf("Expira em %d dias (%s) — programar renovacao", diasParaExpirar, naoApos.Format("02/01/2006")), diasParaExpirar
	default:
		return StatusOK, fmt.Sprintf("Valido ate %s (%d dias restantes)", naoApos.Format("02/01/2006"), diasParaExpirar), diasParaExpirar
	}
}

// descreverVersaoTLS converte o constante uint16 da versao em string legivel
// + status (FAIL para 1.0/1.1, OK para 1.2+).
func descreverVersaoTLS(versao uint16) (string, Status, string) {
	switch versao {
	case tls.VersionTLS10:
		return "TLS 1.0", StatusFail, "TLS 1.0 — DEPRECATED desde 2020. Insegura, deveria ser removida."
	case tls.VersionTLS11:
		return "TLS 1.1", StatusFail, "TLS 1.1 — DEPRECATED desde 2020. Insegura, deveria ser removida."
	case tls.VersionTLS12:
		return "TLS 1.2", StatusOK, "TLS 1.2 — versao segura"
	case tls.VersionTLS13:
		return "TLS 1.3", StatusOK, "TLS 1.3 — versao mais segura e rapida"
	default:
		return fmt.Sprintf("0x%x", versao), StatusWarn, "Versao TLS desconhecida"
	}
}

// detectarAntivirusMITM percorre a cadeia procurando issuers que indiquem que
// um antivirus local esta interceptando TLS. Retorna o nome do antivirus
// detectado ou "" se a cadeia parece limpa.
func detectarAntivirusMITM(cadeia []*x509.Certificate) string {
	marcadores := map[string]string{
		"bitdefender": "Bitdefender",
		"kaspersky":   "Kaspersky",
		"norton":      "Norton/Symantec",
		"symantec":    "Norton/Symantec",
		"avg":         "AVG",
		"avast":       "Avast",
		"sophos":      "Sophos",
		"eset":        "ESET",
		"trend micro": "Trend Micro",
		"mcafee":      "McAfee",
		"fortinet":    "Fortinet",
		"f-secure":    "F-Secure",
	}
	for _, cert := range cadeia {
		issuer := strings.ToLower(cert.Issuer.CommonName + " " + strings.Join(cert.Issuer.Organization, " "))
		for marcador, nome := range marcadores {
			if strings.Contains(issuer, marcador) {
				return nome
			}
		}
	}
	return ""
}
