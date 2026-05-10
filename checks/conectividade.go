package checks

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"
)

// CheckDNS resolve o dominio da instancia.
func CheckDNS(instancia string) Resultado {
	inicio := time.Now()
	r := Resultado{Categoria: "Conectividade", Nome: "Resolucao DNS"}
	host := fmt.Sprintf("%s.vucasolution.com.br", instancia)

	ips, err := net.LookupIP(host)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Falha ao resolver %s: %s", host, err.Error())
		return r
	}
	listaIps := make([]string, 0, len(ips))
	for _, ip := range ips {
		listaIps = append(listaIps, ip.String())
	}
	r.Status = StatusOK
	r.Mensagem = fmt.Sprintf("%s resolveu para %v", host, listaIps)
	r.Detalhes = map[string]interface{}{"host": host, "ips": listaIps}
	return r
}

// CheckHTTPS faz uma requisicao HEAD para o dominio da instancia.
func CheckHTTPS(instancia string) Resultado {
	inicio := time.Now()
	r := Resultado{Categoria: "Conectividade", Nome: "HTTPS"}
	url := fmt.Sprintf("https://%s.vucasolution.com.br/", instancia)

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
		},
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "vuca-infra-diagnostico/1.0")
	resp, err := client.Do(req)
	r.DuracaoMs = time.Since(inicio).Milliseconds()
	if err != nil {
		r.Status = StatusFail
		r.Mensagem = fmt.Sprintf("Falha ao acessar %s: %s", url, err.Error())
		return r
	}
	defer resp.Body.Close()
	r.Detalhes = map[string]interface{}{"url": url, "status_code": resp.StatusCode}
	if resp.StatusCode >= 500 {
		r.Status = StatusWarn
		r.Mensagem = fmt.Sprintf("Servidor respondeu %d", resp.StatusCode)
	} else {
		r.Status = StatusOK
		r.Mensagem = fmt.Sprintf("Resposta %d em %dms", resp.StatusCode, r.DuracaoMs)
	}
	return r
}

// CheckLatencia mede latencia TCP fazendo N conexoes ao host:443.
func CheckLatencia(instancia string, amostras int) Resultado {
	r := Resultado{Categoria: "Conectividade", Nome: "Latencia / Perda de pacote"}
	host := fmt.Sprintf("%s.vucasolution.com.br:443", instancia)

	if amostras <= 0 {
		amostras = 10
	}
	var sucessos int
	var totalMs int64
	var maxMs int64
	var minMs int64 = -1

	inicio := time.Now()
	for i := 0; i < amostras; i++ {
		t0 := time.Now()
		conn, err := net.DialTimeout("tcp", host, 3*time.Second)
		dur := time.Since(t0).Milliseconds()
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
		}
	}
	r.DuracaoMs = time.Since(inicio).Milliseconds()

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
