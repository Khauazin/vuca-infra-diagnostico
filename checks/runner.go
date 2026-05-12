package checks

import (
	"fmt"
	"log"
	"runtime/debug"
	"time"
)

// Executar roda todos os checks em sequencia e devolve o relatorio.
// Envia cada resultado pelo canal opcional `progresso` se nao for nil.
//
// Fluxo:
//   1. CheckSistema sempre roda (info do SO).
//   2. CheckDNS sempre roda — valida a saude do DNS local do cliente
//      (independente de instancia). Nao e' gate.
//   3. Se instancia foi informada:
//      a. CheckHTTPS — autoridade sobre a existencia da instancia. E' o GATE.
//      b. Se HTTPS retornar FAIL, emite sentinela "Diagnostico interrompido"
//         e pula todos os checks subsequentes (Latencia/RabbitMQ/VucaLocal/Impressoras).
//      c. Se passou, roda CheckLatencia em seguida.
//   4. Se instancia nao foi informada, emite aviso de "Validacao parcial" e
//      segue para os checks de rede interna sem o gate.
//   5. RabbitMQ + VucaLocal + Impressoras rodam por ultimo, exceto se o gate
//      barrou.
func Executar(cfg Config, eventos chan<- Evento) Relatorio {
	rel := Relatorio{
		GeradoEm: time.Now(),
		Config:   cfg,
	}

	enviar := func(ev Evento) {
		if eventos != nil {
			eventos <- ev
		}
	}

	// add envia um Resultado pronto (usado para sentinelas que nao tem sub-passos).
	add := func(res Resultado) {
		rel.Resultados = append(rel.Resultados, res)
		enviar(Evento{Tipo: "resultado", Dados: res})
	}

	// runCheck envia check_inicio, executa o check com callback de sub-passos
	// (que sao forwardeados como eventos "subpasso"), e por fim envia o
	// resultado completo. Retorna o Resultado para o caller (necessario para
	// gate logic).
	//
	// Tambem loga inicio/fim de cada check + recover de panic (se um check
	// quebrar, registra no log e devolve um Resultado de FAIL em vez de
	// derrubar o servidor).
	runCheck := func(categoria, nome string, executar func(emit func(SubPasso)) Resultado) Resultado {
		log.Printf("[CHECK] inicio: %s / %s", categoria, nome)
		inicio := time.Now()

		enviar(Evento{Tipo: "check_inicio", Dados: Resultado{
			Categoria: categoria,
			Nome:      nome,
			Status:    StatusInfo,
			Mensagem:  "Executando...",
		}})
		emit := func(sp SubPasso) {
			enviar(Evento{Tipo: "subpasso", Dados: SubPassoEvento{
				CheckCategoria: categoria,
				CheckNome:      nome,
				SubPasso:       sp,
			}})
		}

		var res Resultado
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					stack := string(debug.Stack())
					log.Printf("[PANIC] check %s / %s entrou em panic: %v\n%s",
						categoria, nome, rec, stack)
					res = Resultado{
						Categoria: categoria,
						Nome:      nome,
						Status:    StatusFail,
						Mensagem:  fmt.Sprintf("Erro interno no check (registrado no log): %v", rec),
						DuracaoMs: time.Since(inicio).Milliseconds(),
					}
				}
			}()
			res = executar(emit)
		}()

		rel.Resultados = append(rel.Resultados, res)
		enviar(Evento{Tipo: "resultado", Dados: res})
		log.Printf("[CHECK] fim: %s / %s — status=%s, %dms",
			categoria, nome, res.Status, res.DuracaoMs)
		return res
	}

	finalizar := func() Relatorio {
		if eventos != nil {
			close(eventos)
		}
		return rel
	}

	// (1) Info do SO
	runCheck("Sistema", "Sistema operacional", func(emit func(SubPasso)) Resultado {
		return CheckSistema(emit)
	})

	// (1.1) Informacoes da rede local — interfaces, gateway, MTU, identidade
	// externa, banda. Ajudam o tecnico a contextualizar o ambiente do cliente
	// antes dos checks de conectividade. Mistura de info e checks de capacidade.
	runCheck("Rede Local", "Interfaces e enderecamento IP", func(emit func(SubPasso)) Resultado {
		return CheckRedeLocalInfo(emit)
	})
	runCheck("Rede Local", "Gateway local (roteador)", func(emit func(SubPasso)) Resultado {
		return CheckGatewayLocal(emit)
	})
	runCheck("Rede Local", "MTU / Fragmentacao", func(emit func(SubPasso)) Resultado {
		return CheckMTU(emit)
	})
	runCheck("Rede Local", "Identidade externa (IP publico)", func(emit func(SubPasso)) Resultado {
		return CheckIPPublico(emit)
	})
	runCheck("Rede Local", "Banda de download (10MB)", func(emit func(SubPasso)) Resultado {
		return CheckBanda(emit)
	})

	// (1.2) Ambiente local — detecta interferencias silenciosas no PC do tecnico
	// que podem afetar todo o resto (proxy, drift de relogio, perfil de energia).
	runCheck("Ambiente", "Proxy / VPN ativo", func(emit func(SubPasso)) Resultado {
		return CheckProxyAtivo(emit)
	})
	runCheck("Ambiente", "Relogio do sistema", func(emit func(SubPasso)) Resultado {
		return CheckRelogio(emit)
	})
	runCheck("Ambiente", "Perfil de energia", func(emit func(SubPasso)) Resultado {
		return CheckPerfilEnergia(emit)
	})

	// (2) DNS local sempre roda — valida o servidor DNS do cliente.
	runCheck("Conectividade", "Resolucao DNS", func(emit func(SubPasso)) Resultado {
		return CheckDNS(cfg.Instancia, emit)
	})

	// (3) Gate HTTPS (so' se instancia informada).
	gateInterrompido := false
	if cfg.Instancia == "" {
		add(Resultado{
			Categoria: "Validacao",
			Nome:      "Validacao parcial",
			Status:    StatusWarn,
			Mensagem:  "Instancia nao informada — ambiente nao foi validado por completo. Os checks de rede interna (RabbitMQ, VucaLocal, impressoras) continuam, mas a instancia e o HTTPS publico nao foram conferidos.",
		})
	} else {
		httpsRes := runCheck("Conectividade", "HTTPS", func(emit func(SubPasso)) Resultado {
			return CheckHTTPS(cfg.Instancia, emit)
		})

		if httpsRes.Status == StatusFail {
			add(Resultado{
				Categoria: "Validacao",
				Nome:      "Diagnostico interrompido",
				Status:    StatusFail,
				Mensagem:  "Instancia/URL nao validada — o HTTPS confirmou que essa instancia nao existe no cluster. Os demais checks foram pulados para evitar falsos positivos. Confirme o nome da instancia e tente novamente.",
			})
			gateInterrompido = true
		} else {
			runCheck("Conectividade", "Latencia / Perda de pacote", func(emit func(SubPasso)) Resultado {
				return CheckLatencia(cfg.Instancia, 10, emit)
			})
			runCheck("Conectividade", "Tempos de conexao (HTTPS por fase)", func(emit func(SubPasso)) Resultado {
				return CheckHTTPSFases(cfg.Instancia, emit)
			})
			runCheck("Conectividade", "Certificado TLS / SSL", func(emit func(SubPasso)) Resultado {
				return CheckTLS(cfg.Instancia, emit)
			})
			runCheck("Conectividade", "Consistencia HTTPS (3 requisicoes)", func(emit func(SubPasso)) Resultado {
				return CheckHTTPSConsistencia(cfg.Instancia, emit)
			})
			runCheck("Rede Local", "Latencia estendida (50 amostras / 30s)", func(emit func(SubPasso)) Resultado {
				return CheckLatenciaLonga(cfg.Instancia, emit)
			})
		}
	}

	if gateInterrompido {
		return finalizar()
	}

	// (4) Rede interna do cliente
	rabbitHost := cfg.RabbitMQ.Host
	if rabbitHost == "" {
		rabbitHost = "localhost"
	}
	// Sinaliza se o usuario configurou RabbitMQ explicitamente. Quando nao
	// configurou (host = "localhost" default + sem credenciais), pulamos os
	// checks pesados (AMQP handshake, Heartbeat 10s, Management API, Queues)
	// para nao gerar varios FAIL espurios.
	rabbitConfigurado := rabbitHost != "" && rabbitHost != "localhost"
	rabbitDeveTestar := rabbitConfigurado || cfg.RabbitMQ.Usuario != ""

	for _, p := range cfg.RabbitMQ.Portas {
		porta := p
		hostPorta := rabbitHost + ":" + itoa(porta)
		runCheck("RabbitMQ", hostPorta, func(emit func(SubPasso)) Resultado {
			return CheckPortaTCP("RabbitMQ", hostPorta, rabbitHost, porta, emit)
		})
		// AMQP + Heartbeat so' rodam se o usuario configurou RabbitMQ
		// (porta 5672 espera AMQP). TCP check acima ja' sinaliza problema.
		if porta == 5672 && rabbitDeveTestar {
			runCheck("RabbitMQ", fmt.Sprintf("Protocolo AMQP em %s:%d", rabbitHost, porta), func(emit func(SubPasso)) Resultado {
				return CheckRabbitMQAMQP(rabbitHost, porta, emit)
			})
			runCheck("RabbitMQ", "Estabilidade da conexao (10s idle)", func(emit func(SubPasso)) Resultado {
				return CheckRabbitMQHeartbeat(rabbitHost, porta, emit)
			})
		}
	}

	// (A) Management API + (E) Queues do cliente — usa porta 15672
	if rabbitDeveTestar {
		runCheck("RabbitMQ", "Management API (porta 15672)", func(emit func(SubPasso)) Resultado {
			return CheckRabbitMQManagement(rabbitHost, cfg.RabbitMQ.Usuario, cfg.RabbitMQ.Senha, emit)
		})
		if cfg.RabbitMQ.Usuario != "" && cfg.Instancia != "" {
			runCheck("RabbitMQ", "Queues do cliente", func(emit func(SubPasso)) Resultado {
				return CheckRabbitMQQueues(rabbitHost, cfg.RabbitMQ.Usuario, cfg.RabbitMQ.Senha, cfg.Instancia, "", cfg.Impressoras, emit)
			})
		}
	}

	runCheck("VucaLocal", "Endpoint VucaLocal", func(emit func(SubPasso)) Resultado {
		return CheckVucaLocal(cfg.VucaLocal, emit)
	})

	for _, imp := range cfg.Impressoras {
		impCopy := imp
		nomeImp := impCopy.Nome
		if nomeImp == "" {
			nomeImp = impCopy.IP
		}
		runCheck("Impressoras", nomeImp, func(emit func(SubPasso)) Resultado {
			return CheckImpressora(impCopy, emit)
		})
		// Valida que a impressora realmente fala ESC/POS, nao so' TCP.
		runCheck("Impressoras", nomeImp+" (ESC/POS)", func(emit func(SubPasso)) Resultado {
			return CheckImpressoraESC(impCopy, emit)
		})
	}

	// (5) Testes customizados de porta TCP (definidos pelo tecnico no formulario)
	for _, tp := range cfg.PortasCustomizadas {
		nome := tp.Nome
		if nome == "" {
			nome = fmt.Sprintf("%s:%d", tp.Host, tp.Porta)
		}
		hostCopy := tp.Host
		portaCopy := tp.Porta
		runCheck("Portas customizadas", nome, func(emit func(SubPasso)) Resultado {
			return CheckPortaTCP("Portas customizadas", nome, hostCopy, portaCopy, emit)
		})
	}

	return finalizar()
}

func joinStrings(s []string, sep string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += sep
		}
		out += v
	}
	return out
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
