package checks

import "time"

// Executar roda todos os checks em sequencia e devolve o relatorio.
// Envia cada resultado pelo canal opcional `progresso` se nao for nil.
func Executar(cfg Config, progresso chan<- Resultado) Relatorio {
	rel := Relatorio{
		GeradoEm: time.Now(),
		Config:   cfg,
	}
	add := func(res Resultado) {
		rel.Resultados = append(rel.Resultados, res)
		if progresso != nil {
			progresso <- res
		}
	}

	add(CheckSistema())

	if cfg.Instancia != "" {
		add(CheckDNS(cfg.Instancia))
		add(CheckHTTPS(cfg.Instancia))
		add(CheckLatencia(cfg.Instancia, 10))
	}

	rabbitHost := cfg.RabbitMQ.Host
	if rabbitHost == "" {
		rabbitHost = "localhost"
	}
	for _, p := range cfg.RabbitMQ.Portas {
		add(CheckPortaTCP("RabbitMQ", rabbitHost+":"+itoa(p), rabbitHost, p))
	}

	add(CheckVucaLocal(cfg.VucaLocal))

	for _, imp := range cfg.Impressoras {
		add(CheckImpressora(imp))
	}

	if progresso != nil {
		close(progresso)
	}
	return rel
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
