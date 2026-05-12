package checks

import "strings"

// Explicacao contem o detalhamento didatico de um Resultado. Usado no relatorio
// HTML para explicar (em linguagem comum) o que foi testado, o que o resultado
// significa, e quando houver problema, possiveis causas e acoes a tomar.
type Explicacao struct {
	OQueSignifica string   // descricao estatica do teste (mesma para todos os status)
	Interpretacao string   // significado do resultado especifico (varia por status)
	Causas        []string // causas provaveis quando WARN/FAIL; vazio para OK/INFO
	Acoes         []string // acoes sugeridas; pode ser vazio para OK
}

// variantePorStatus e' o conteudo que muda dependendo do status do Resultado.
type variantePorStatus struct {
	Interpretacao string
	Causas        []string
	Acoes         []string
}

// configCheck agrupa a descricao estatica do teste + as variantes por status.
type configCheck struct {
	OQueSignifica string
	Variantes     map[Status]variantePorStatus
}

// ExplicarResultado retorna a explicacao didatica de um Resultado, combinando
// a descricao estatica do teste com o conteudo especifico do status.
func ExplicarResultado(r Resultado) Explicacao {
	tipo := classificarTipoCheck(r)
	cfg, ok := explicacoesPorTipo[tipo]
	if !ok {
		cfg = explicacoesPorTipo["generico"]
	}
	v := cfg.Variantes[r.Status]
	if v.Interpretacao == "" {
		v = fallbackPorStatus(r.Status)
	}
	return Explicacao{
		OQueSignifica: cfg.OQueSignifica,
		Interpretacao: v.Interpretacao,
		Causas:        v.Causas,
		Acoes:         v.Acoes,
	}
}

// classificarTipoCheck determina o "tipo" de um check a partir de sua categoria
// e nome. Isso permite que cards com nomes dinamicos (ex: impressoras com nomes
// proprios, portas customizadas) compartilhem a mesma explicacao.
func classificarTipoCheck(r Resultado) string {
	cat := r.Categoria
	nome := r.Nome
	switch cat {
	case "Sistema":
		return "sistema"
	case "Rede Local":
		switch {
		case strings.Contains(nome, "Interfaces"):
			return "rede-local-interfaces"
		case strings.Contains(nome, "Gateway"):
			return "rede-local-gateway"
		case strings.Contains(nome, "MTU"):
			return "rede-local-mtu"
		case strings.Contains(nome, "IP publico"):
			return "rede-local-ip-publico"
		case strings.Contains(nome, "Banda"):
			return "rede-local-banda"
		case strings.Contains(nome, "Latencia estendida"):
			return "rede-local-latencia-longa"
		}
	case "Ambiente":
		switch {
		case strings.Contains(nome, "Proxy"):
			return "ambiente-proxy"
		case strings.Contains(nome, "Relogio"):
			return "ambiente-relogio"
		case strings.Contains(nome, "Perfil"):
			return "ambiente-perfil-energia"
		}
	case "Conectividade":
		switch {
		case strings.Contains(nome, "DNS"):
			return "conectividade-dns"
		case strings.Contains(nome, "por fase"):
			return "conectividade-fases"
		case strings.Contains(nome, "Latencia"):
			return "conectividade-latencia"
		case strings.Contains(nome, "TLS"):
			return "conectividade-tls"
		case strings.Contains(nome, "Consistencia"):
			return "conectividade-consistencia"
		case nome == "HTTPS":
			return "conectividade-https"
		}
	case "Validacao":
		switch {
		case strings.Contains(nome, "parcial"):
			return "validacao-parcial"
		case strings.Contains(nome, "interrompido"):
			return "validacao-interrompido"
		}
	case "RabbitMQ":
		if strings.Contains(nome, "Protocolo AMQP") {
			return "rabbitmq-amqp"
		}
		return "rabbitmq-porta"
	case "VucaLocal":
		return "vucalocal"
	case "Impressoras":
		if strings.Contains(nome, "ESC/POS") {
			return "impressora-esc"
		}
		return "impressora-tcp"
	case "Portas customizadas":
		return "porta-customizada"
	}
	return "generico"
}

// fallbackPorStatus retorna uma variante generica usada quando nao ha entrada
// especifica para o tipo+status. Garante que SEMPRE existe interpretacao
// disponivel no relatorio.
func fallbackPorStatus(s Status) variantePorStatus {
	switch s {
	case StatusOK:
		return variantePorStatus{
			Interpretacao: "O teste passou sem problemas.",
		}
	case StatusWarn:
		return variantePorStatus{
			Interpretacao: "O teste passou mas com um aviso — vale conferir o detalhe na mensagem.",
			Acoes:         []string{"Revisar a mensagem tecnica acima e avaliar se a causa exige acao."},
		}
	case StatusFail:
		return variantePorStatus{
			Interpretacao: "O teste falhou.",
			Causas:        []string{"A causa esta descrita na mensagem tecnica acima."},
			Acoes:         []string{"Investigar a falha apontada e corrigir antes de operar."},
		}
	case StatusInfo:
		return variantePorStatus{
			Interpretacao: "Resultado informativo — nao indica problema nem sucesso, apenas contextualiza.",
		}
	}
	return variantePorStatus{Interpretacao: "Sem detalhes adicionais."}
}

// explicacoesPorTipo e' o catalogo de explicacoes didaticas para cada tipo de
// check. Para CADA tipo, listamos a descricao estatica e variantes por status.
var explicacoesPorTipo = map[string]configCheck{
	// =====================================================================
	// SISTEMA
	// =====================================================================
	"sistema": {
		OQueSignifica: "Levanta informacoes do PC onde o diagnostico esta rodando: sistema operacional, arquitetura, numero de processadores e fuso horario. Serve apenas como contexto para interpretar os outros testes.",
		Variantes: map[Status]variantePorStatus{
			StatusInfo: {
				Interpretacao: "Sistema identificado com sucesso. Esses dados aparecem nos detalhes apenas para referencia.",
				Acoes:         []string{"Nenhuma acao necessaria — informacao apenas contextual."},
			},
		},
	},

	// =====================================================================
	// REDE LOCAL
	// =====================================================================
	"rede-local-interfaces": {
		OQueSignifica: "Lista as placas de rede ativas no PC (Wi-Fi, Ethernet, virtuais) e o IP/mascara de cada uma. Ajuda a identificar em qual rede o PC esta e se o enderecamento esta correto.",
		Variantes: map[Status]variantePorStatus{
			StatusInfo: {
				Interpretacao: "Pelo menos uma interface de rede esta ativa e o PC consegue se comunicar na rede local.",
			},
			StatusWarn: {
				Interpretacao: "Nenhuma interface ativa com IPv4 foi encontrada — sintoma de PC desconectado.",
				Causas: []string{
					"Cabo de rede desconectado",
					"Wi-Fi desligado ou fora de alcance",
					"Placa de rede desabilitada nas configuracoes do Windows",
					"Driver de rede com problema ou desinstalado",
				},
				Acoes: []string{
					"Verifique se o cabo de rede esta firme no PC e no switch/roteador",
					"Confirme que o Wi-Fi esta ligado e conectado a rede correta",
					"Abra o Gerenciador de Dispositivos e verifique se a placa de rede esta sem icone de aviso",
				},
			},
		},
	},

	"rede-local-gateway": {
		OQueSignifica: "Testa se o roteador da rede (o equipamento que faz a 'ponte' entre a rede da loja e a internet) esta respondendo e em quanto tempo. Latencia alta no roteador deixa toda a rede lenta.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "O roteador esta respondendo rapidamente. A saida da rede local esta saudavel.",
			},
			StatusInfo: {
				Interpretacao: "O roteador foi identificado mas nao respondeu em portas comuns (53, 80, 443). Isso e' normal em alguns modelos que bloqueiam acesso ao painel administrativo — nao indica problema.",
			},
			StatusWarn: {
				Interpretacao: "O roteador esta com latencia elevada — pode estar sobrecarregado ou com problema.",
				Causas: []string{
					"Roteador muito antigo, com firmware desatualizado ou superdimensionado",
					"Muitos dispositivos conectados simultaneamente saturando o roteador",
					"Roteador em local sem ventilacao (aquecendo)",
					"Defeito fisico do equipamento (e' candidato a troca)",
				},
				Acoes: []string{
					"Reinicie o roteador (tira da tomada, espera 10 segundos, religa)",
					"Verifique se o roteador esta em local ventilado e sem objetos em cima",
					"Conte quantos dispositivos estao conectados — pode estar excedendo o limite do modelo",
					"Considere substituir por um modelo mais robusto se o problema persistir",
				},
			},
		},
	},

	"rede-local-mtu": {
		OQueSignifica: "Descobre o tamanho maximo de pacote que a rede consegue enviar sem fragmentacao. MTU menor que 1500 e' problema classico em conexoes PPPoE/fibra mal configuradas e causa lentidao silenciosa em uploads.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "MTU 1500 funciona normalmente — a rede transporta pacotes no tamanho padrao Ethernet sem precisar quebrar.",
			},
			StatusInfo: {
				Interpretacao: "Nao foi possivel determinar o MTU automaticamente neste sistema.",
				Acoes: []string{
					"Em ambiente Windows, o teste usa o comando 'ping' do sistema — verifique se ele esta acessivel",
					"Em outros sistemas operacionais, este teste ainda nao foi implementado",
				},
			},
			StatusWarn: {
				Interpretacao: "MTU efetivo da rede esta abaixo do padrao 1500. Isso quase sempre indica uma conexao PPPoE/VPN configurada errada na operadora.",
				Causas: []string{
					"Conexao PPPoE (fibra) com MTU mal configurado no roteador",
					"VPN intermediaria (ex: SD-WAN corporativa, GRE tunnel) reduzindo MTU",
					"Provedor de internet usando encapsulamento extra que reduz o tamanho util",
				},
				Acoes: []string{
					"Verifique a configuracao de MTU no painel admin do roteador (procure por 'MTU' ou 'PPPoE')",
					"Defina MTU 1500 manualmente se a opcao estiver disponivel",
					"Acione o suporte da operadora informando 'MTU menor que 1500 esta causando lentidao em uploads'",
				},
			},
		},
	},

	"rede-local-ip-publico": {
		OQueSignifica: "Descobre qual IP publico a rede da loja esta usando para sair na internet. Util para o tecnico identificar a operadora e correlacionar com problemas conhecidos (ex: 'sempre da problema em cliente Vivo Fibra').",
		Variantes: map[Status]variantePorStatus{
			StatusInfo: {
				Interpretacao: "IP publico identificado com sucesso. O codigo do datacenter Cloudflare proximo (ex: GRU, GIG) indica geografia aproximada da saida.",
			},
			StatusWarn: {
				Interpretacao: "Nao foi possivel identificar o IP publico — ha algum bloqueio na saida HTTPS.",
				Causas: []string{
					"Firewall bloqueando saida HTTPS (porta 443) para qualquer destino externo",
					"Servidor DNS local quebrado (nao resolve cloudflare.com)",
					"Conexao com a internet temporariamente fora",
				},
				Acoes: []string{
					"Verifique se outros sites HTTPS abrem normalmente no PC",
					"Se nao abrem, o problema e' geral de internet — chame a operadora",
					"Se abrem, ha bloqueio especifico (firewall, antivirus, proxy) que precisa ser investigado",
				},
			},
		},
	},

	"rede-local-banda": {
		OQueSignifica: "Mede a velocidade de download baixando um arquivo de 10MB de um servidor publico. Em lojas com muitos tablets, a banda precisa ser suficiente para todos operarem simultaneamente.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Banda suficiente para operacao normal — a internet aguenta varios dispositivos sem problemas.",
			},
			StatusWarn: {
				Interpretacao: "Banda limitada — funciona, mas pode formar fila em horarios de pico ou com muitos dispositivos simultaneos.",
				Causas: []string{
					"Plano de internet contratado e' modesto para o tamanho da operacao",
					"Outros dispositivos consumindo banda na hora do teste (download, streaming)",
					"Roteador limitando velocidade no Wi-Fi (rede 2.4GHz sobrecarregada)",
					"Distancia/obstaculos atrapalhando o sinal Wi-Fi onde o teste rodou",
				},
				Acoes: []string{
					"Considere aumentar o plano da internet se a operacao tiver mais de 10 dispositivos",
					"Faca o teste novamente em horarios diferentes para confirmar",
					"Se possivel, use cabo de rede em vez de Wi-Fi durante o teste",
				},
			},
			StatusFail: {
				Interpretacao: "Banda criticamente baixa — nao vai suportar a operacao com varios tablets.",
				Causas: []string{
					"Plano de internet muito abaixo do necessario",
					"Saturacao do link no momento do teste",
					"Problema fisico/operacional com a conexao (modem com defeito, cabeamento ruim)",
				},
				Acoes: []string{
					"Aumente urgentemente o plano de internet — esta abaixo do minimo recomendado",
					"Acione a operadora se o plano contratado nao bate com o teste",
					"Considere internet redundante (link secundario) para lojas que nao podem parar",
				},
			},
		},
	},

	"rede-local-latencia-longa": {
		OQueSignifica: "Faz 50 testes de conexao TCP espalhados em 30 segundos para detectar instabilidade na rede. Mede jitter (variacao do tempo de resposta) e perda de pacote — sintomas tipicos de Wi-Fi saturado.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Rede estavel ao longo dos 30 segundos — sem variacoes ou perdas significativas.",
			},
			StatusWarn: {
				Interpretacao: "Rede com instabilidade moderada — pode causar lentidao ocasional ou requisicoes que demoram mais que o normal.",
				Causas: []string{
					"Wi-Fi saturado ou com interferencia (canal congestionado)",
					"Uplink da operadora oscilando",
					"NAT do roteador com tempo limite curto (derrubando conexoes ociosas)",
					"Outros dispositivos consumindo banda em rajadas",
				},
				Acoes: []string{
					"Verifique se o Wi-Fi esta em canal pouco congestionado (use app de scan)",
					"Considere usar Wi-Fi 5GHz em vez de 2.4GHz para dispositivos criticos",
					"Reinicie o roteador para liberar tabela NAT",
				},
			},
			StatusFail: {
				Interpretacao: "Instabilidade critica — a rede esta perdendo pacotes ou variando muito. Vai causar travamentos visiveis.",
				Causas: []string{
					"Wi-Fi saturado, interferencia eletromagnetica severa",
					"Uplink da operadora com problemas (provavelmente fora do esperado no contrato)",
					"Roteador travado/superaquecido/com defeito",
					"Cabeamento de rede com problema (cabo dobrado, conector mal crimpado)",
				},
				Acoes: []string{
					"Reinicie o roteador imediatamente",
					"Acione a operadora — perda alta nao e' aceitavel em rede de operacao",
					"Verifique cabos de rede entre roteador e switch (recrimpar se necessario)",
				},
			},
		},
	},

	// =====================================================================
	// AMBIENTE
	// =====================================================================
	"ambiente-proxy": {
		OQueSignifica: "Verifica se ha um servidor proxy configurado entre o PC e a internet — seja por variavel de ambiente ou via configuracao do Windows. Proxy oculto pode interceptar e alterar requisicoes silenciosamente, causando problemas dificeis de diagnosticar.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Nenhum proxy detectado — as conexoes saem diretamente para a internet.",
			},
			StatusWarn: {
				Interpretacao: "Um servidor proxy esta configurado no sistema. Isso nem sempre e' problema, mas e' importante saber que TODAS as conexoes do PC passam por ele.",
				Causas: []string{
					"Proxy de rede corporativa (comum em redes empresariais)",
					"Software de seguranca (Bitdefender, antivirus) configurando proxy local",
					"Configuracao antiga deixada de instalacao previa",
					"VPN ativa que setou as variaveis HTTP_PROXY/HTTPS_PROXY",
				},
				Acoes: []string{
					"Confirme com o cliente se o uso do proxy e' intencional",
					"Se nao for, remova: Configuracoes do Windows -> Rede e Internet -> Proxy",
					"Verifique tambem variaveis de ambiente (HTTP_PROXY, HTTPS_PROXY) no Painel de Controle",
				},
			},
		},
	},

	"ambiente-relogio": {
		OQueSignifica: "Compara o horario do PC com o horario de um servidor confiavel na internet. Drift (diferenca) grande quebra autenticacao HTTPS, validacao OAuth e assinatura de webhooks — coisas que aparentam ser 'erro de conexao' mas na verdade sao relogio errado.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Relogio do PC esta sincronizado com a internet — drift menor que 5 segundos. Nenhuma acao necessaria.",
			},
			StatusInfo: {
				Interpretacao: "Nao foi possivel comparar o relogio porque o servidor de referencia esta inacessivel.",
				Acoes: []string{
					"Verifique se outras requisicoes HTTPS estao funcionando",
					"Se a internet estiver fora, este teste so vai funcionar quando voltar",
				},
			},
			StatusWarn: {
				Interpretacao: "Relogio com drift acima do ideal (5-30 segundos). Ainda nao quebra nada, mas pode comecar a quebrar com qualquer aumento.",
				Causas: []string{
					"NTP (sincronizacao automatica de hora) desativado nas configuracoes do Windows",
					"Servidor NTP padrao bloqueado pelo firewall do cliente",
					"PC ficou desligado por muito tempo e o relogio interno do BIOS dessincronizou",
				},
				Acoes: []string{
					"Va em Configuracoes -> Hora e Idioma -> 'Sincronizar agora'",
					"Confirme que 'Definir hora automaticamente' esta ligado",
					"Se nao sincronizar, troque o servidor NTP para 'pool.ntp.org' ou 'a.ntp.br'",
				},
			},
			StatusFail: {
				Interpretacao: "Drift critico (>30 segundos). Vai quebrar autenticacao TLS, login, assinaturas — varios sistemas vao falhar com erro generico.",
				Causas: []string{
					"NTP desligado e relogio sem sincronizar ha tempo",
					"Bateria do BIOS/CMOS da placa-mae fraca/sem carga (PC reseta hora ao desligar)",
					"Fuso horario errado nas configuracoes do Windows",
				},
				Acoes: []string{
					"URGENTE: Ajuste manualmente a hora e o fuso horario do Windows",
					"Force sincronizacao NTP imediatamente",
					"Se o problema voltar ao reiniciar o PC, troque a bateria do CMOS da placa-mae",
				},
			},
		},
	},

	"ambiente-perfil-energia": {
		OQueSignifica: "Verifica qual perfil de energia esta ativo no Windows. Perfis de economia podem desligar a placa de rede para 'economizar', causando latencia e drops de Wi-Fi intermitentes — sintoma classico de 'Wi-Fi cai sozinho' em PC de caixa.",
		Variantes: map[Status]variantePorStatus{
			StatusInfo: {
				Interpretacao: "Perfil identificado — esta compativel com operacao continua de rede.",
			},
			StatusWarn: {
				Interpretacao: "PC esta no perfil de 'Economia de energia'. Pode causar latencia eventual em Wi-Fi e USB, alem de drops de conexao.",
				Causas: []string{
					"Notebook configurado para economizar bateria quando na tomada",
					"Configuracao padrao deixada de fabrica em alguns notebooks",
					"Politica de TI corporativa aplicada no PC",
				},
				Acoes: []string{
					"Mude para 'Equilibrado' ou 'Alto desempenho' em: Painel de Controle -> Opcoes de Energia",
					"Em PC de caixa, recomenda-se 'Alto desempenho' sempre",
					"Em notebooks no dock, configure 'Alto desempenho' quando na tomada",
				},
			},
		},
	},

	// =====================================================================
	// CONECTIVIDADE
	// =====================================================================
	"conectividade-dns": {
		OQueSignifica: "Testa a saude do DNS local — o servico que 'traduz' nomes (vucasolution.com.br) em IPs. DNS lento ou quebrado faz aplicacoes parecerem que travaram, pois cada requisicao precisa primeiro resolver o nome.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "DNS local saudavel — resolve nomes externos rapidamente e o nome da instancia foi confirmado.",
			},
			StatusWarn: {
				Interpretacao: "DNS funcionando mas com algum aviso — resolveu mas com latencia elevada OU via wildcard (cluster nao confirma instancia) OU divergencia entre servidores.",
				Causas: []string{
					"DNS da operadora antigo/lento (causa 'tablet abre devagar')",
					"DNS local com cache zoado (resoluces antigas em cache)",
					"DNS do roteador com problema (trocar para 1.1.1.1 ou 8.8.8.8 normalmente resolve)",
					"DNS hijacking de operadora (responde diferente dos publicos)",
				},
				Acoes: []string{
					"Troque o servidor DNS do PC para 1.1.1.1 ou 8.8.8.8",
					"Limpe o cache DNS local: prompt do Windows -> 'ipconfig /flushdns'",
					"Se o problema for so em uma loja, verifique o DNS configurado no roteador",
				},
			},
			StatusFail: {
				Interpretacao: "DNS local quebrado — nem dominios externos conhecidos resolvem. Nada vai funcionar ate isso ser corrigido.",
				Causas: []string{
					"PC sem conexao com a internet (DNS nao alcanca)",
					"Servidor DNS configurado errado (IP invalido)",
					"DNS local (do roteador) caido ou bloqueado",
					"Firewall do Windows ou antivirus bloqueando consultas DNS (porta 53)",
				},
				Acoes: []string{
					"Verifique se a internet do PC esta funcionando (abre algum site?)",
					"Va em 'Configuracoes de rede' e troque DNS para automatico ou para 1.1.1.1",
					"Reinicie o roteador",
					"Desabilite temporariamente o antivirus para confirmar se nao e' ele bloqueando",
				},
			},
		},
	},

	"conectividade-https": {
		OQueSignifica: "Verifica se a aplicacao web Vuca esta respondendo no endereco configurado para esta loja. Este e' o teste mais importante — confirma que a instancia existe e esta acessivel pela internet publica.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Aplicacao Vuca esta respondendo normalmente — a instancia existe e esta no ar.",
			},
			StatusWarn: {
				Interpretacao: "Aplicacao respondeu, mas com um status HTTP inesperado. Pode ser problema temporario ou configuracao incomum.",
				Causas: []string{
					"Servidor sobrecarregado retornando 503 (Service Unavailable)",
					"Manutencao em andamento no momento do teste",
					"Configuracao especifica da instancia retornando codigo nao usual em '/'",
				},
				Acoes: []string{
					"Tente abrir a URL diretamente no navegador para ver a resposta",
					"Se for 5xx, espere alguns minutos e teste novamente",
					"Se persistir, acione o time de plataforma Vuca",
				},
			},
			StatusFail: {
				Interpretacao: "A aplicacao Vuca NAO esta respondendo neste endereco. Provavelmente o nome da instancia esta errado ou a instancia foi removida.",
				Causas: []string{
					"Nome da instancia digitado errado (typo) no formulario de diagnostico",
					"Instancia ainda nao foi criada na nuvem Vuca para este cliente",
					"Instancia foi removida ou renomeada na nuvem",
					"Falha de rede impedindo o PC de chegar no cluster Vuca",
				},
				Acoes: []string{
					"Confira o nome da instancia (sem dominio, ex: 'lifeboxburger' — nao 'lifeboxburger.vucasolution.com.br')",
					"Confirme com o time Vuca se a instancia existe para este cliente",
					"Se outros testes de rede tambem falharam, o problema e' conectividade geral, nao a instancia",
				},
			},
		},
	},

	"conectividade-latencia": {
		OQueSignifica: "Mede o tempo medio de resposta da nuvem Vuca fazendo 10 conexoes TCP em sequencia. Latencia alta indica que tudo vai parecer 'lento' do ponto de vista do usuario, mesmo que esteja funcionando.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Latencia normal — tempos consistentes e sem perda de pacote.",
			},
			StatusWarn: {
				Interpretacao: "Latencia elevada ou alguma perda de pacote. A operacao funciona mas o usuario vai perceber lentidao.",
				Causas: []string{
					"Distancia geografica grande entre cliente e datacenter Vuca",
					"Roteamento via operadora ruim (peering mal otimizado)",
					"Wi-Fi com sinal fraco no local do PC",
					"Outros dispositivos consumindo banda no momento do teste",
				},
				Acoes: []string{
					"Use cabo de rede em vez de Wi-Fi para PCs criticos (caixa, KDS)",
					"Se possivel, mude o PC para perto do roteador",
					"Se a perda for grande, acione a operadora",
				},
			},
			StatusFail: {
				Interpretacao: "Perda de pacote critica — mais da metade das conexoes falhou. A rede esta seriamente comprometida.",
				Causas: []string{
					"Internet do cliente intermitente ou fora",
					"Roteador travado",
					"Bloqueio de firewall em alguma porta especifica (raro)",
					"Problema fisico na infraestrutura de rede do cliente",
				},
				Acoes: []string{
					"URGENTE: investigue a rede do cliente antes de continuar",
					"Reinicie roteador e modem",
					"Acione a operadora se o problema persistir apos reiniciar",
				},
			},
		},
	},

	"conectividade-fases": {
		OQueSignifica: "Mede separadamente o tempo de cada etapa de uma requisicao HTTPS: resolucao DNS, conexao TCP, handshake TLS e tempo ate o primeiro byte de resposta. Permite diagnosticar EXATAMENTE em que fase esta o gargalo.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Todas as fases dentro do esperado — conexao saudavel.",
			},
			StatusWarn: {
				Interpretacao: "Alguma fase esta lenta. A mensagem indica qual — DNS, TCP, TLS ou TTFB. Operacao funciona mas usuario percebe demora.",
				Causas: []string{
					"DNS lento → operadora ou servidor DNS local ruim",
					"TCP lento → problema de roteamento entre cliente e Vuca",
					"TLS lento → MTU mal configurado, antivirus interceptando ou hardware fraco",
					"TTFB lento → backend Vuca sobrecarregado (raro)",
				},
				Acoes: []string{
					"Olhe a mensagem para identificar QUAL fase esta lenta",
					"Para DNS lento: troque o servidor DNS",
					"Para TLS lento: verifique se ha antivirus interceptando HTTPS",
					"Para TCP/TTFB lentos: pode ser problema de rota da operadora",
				},
			},
			StatusFail: {
				Interpretacao: "Alguma fase esta criticamente lenta ou a conexao excedeu 20 segundos. Isso explica casos extremos do tipo 'aplicacao demora minutos para abrir'.",
				Causas: []string{
					"DNS extremamente lento (servidor DNS local em colapso)",
					"Rota de internet rompida ou redirecionada",
					"Antivirus inspecionando TLS e travando a maquina",
					"Internet do cliente em colapso parcial",
				},
				Acoes: []string{
					"Verifique todos os outros testes — provavelmente varios estao falhando junto",
					"Reinicie roteador e modem",
					"Desabilite antivirus temporariamente para teste",
					"Se nada resolver, e' problema com a operadora — acione",
				},
			},
		},
	},

	"conectividade-tls": {
		OQueSignifica: "Inspeciona o certificado HTTPS da instancia: validade, versao TLS negociada, cipher e cadeia. Tambem detecta se algum antivirus local esta interceptando a conexao HTTPS (TLS MITM).",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Certificado valido, versao TLS moderna e cadeia limpa. Sem sinal de interferencia de antivirus.",
			},
			StatusWarn: {
				Interpretacao: "Certificado proximo de vencer OU antivirus local interceptando TLS. Nao quebra operacao agora mas exige atencao.",
				Causas: []string{
					"Certificado SSL expirando em menos de 30 dias (precisa renovacao em breve)",
					"Antivirus (Bitdefender, Kaspersky, ESET, etc) configurado para 'escanear HTTPS', quebrando a cadeia original",
					"Proxy corporativo interceptando TLS (mais comum em redes empresariais)",
				},
				Acoes: []string{
					"Se for cert expirando: programe renovacao com o time Vuca",
					"Se for antivirus: considere desabilitar 'inspecao de HTTPS' nas configuracoes do antivirus",
					"Antivirus interceptando pode quebrar algumas integracoes Vuca — recomenda-se desabilitar",
				},
			},
			StatusFail: {
				Interpretacao: "Problema critico com TLS — certificado vencido, expirando em menos de 1 semana, ou versao TLS obsoleta (1.0/1.1).",
				Causas: []string{
					"Certificado SSL vencido (nao renovado a tempo)",
					"Servidor configurado com TLS 1.0 ou 1.1 (deprecated desde 2020)",
					"Erro na configuracao da instancia",
				},
				Acoes: []string{
					"URGENTE: renove o certificado SSL imediatamente (acione time Vuca)",
					"Para TLS legado: a instancia precisa ser reconfigurada para usar TLS 1.2+",
					"Cert vencido bloqueia conexao em navegadores e dispositivos modernos",
				},
			},
		},
	},

	"conectividade-consistencia": {
		OQueSignifica: "Faz 3 requisicoes HTTPS seguidas e compara o resultado. Detecta variabilidade no balanceador, instabilidade backend ou casos de 'API que responde 200 mas com erro embutido no body'.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "3 requisicoes seguidas retornaram o mesmo status, tempos consistentes e body limpo. Backend estavel.",
			},
			StatusWarn: {
				Interpretacao: "Detectada inconsistencia entre as 3 requisicoes — pode indicar instabilidade em algum no do cluster, balanceador inconsistente ou API retornando erro em body de 200.",
				Causas: []string{
					"Algum no do cluster com problema, balanceador rodando entre nos saudaveis e doentes",
					"Cold start (algum container subiu na hora do teste e foi mais lento)",
					"API mal-feita que retorna 200 com {error: ...} em vez de status >= 400",
					"Backend em fase de deploy/restart no momento do teste",
				},
				Acoes: []string{
					"Rode o diagnostico novamente em alguns minutos — se persistir, ha problema real",
					"Compare com diagnosticos de outras lojas — se so esta loja tem o problema, e' local",
					"Acione time de plataforma Vuca se a inconsistencia for recorrente",
				},
			},
		},
	},

	// =====================================================================
	// VALIDACAO (sentinelas)
	// =====================================================================
	"validacao-parcial": {
		OQueSignifica: "Aviso emitido quando o tecnico executou o diagnostico sem informar a instancia. Os testes de rede interna (RabbitMQ, VucaLocal, impressoras) ainda rodam, mas a parte de validar a nuvem Vuca foi pulada.",
		Variantes: map[Status]variantePorStatus{
			StatusWarn: {
				Interpretacao: "Diagnostico rodou em modo parcial. Util para testar so a rede interna do cliente, mas nao confirma que a instancia Vuca esta acessivel.",
				Acoes: []string{
					"Para validacao completa, rode o diagnostico de novo informando o nome da instancia",
				},
			},
		},
	},

	"validacao-interrompido": {
		OQueSignifica: "Sinaliza que o diagnostico foi abortado porque a validacao do HTTPS da instancia falhou. Os testes seguintes (latencia, RabbitMQ, VucaLocal, impressoras) foram pulados para evitar falsos positivos.",
		Variantes: map[Status]variantePorStatus{
			StatusFail: {
				Interpretacao: "Os testes posteriores nao foram executados porque a instancia nao foi validada — testar impressora se a instancia nao existe e' perda de tempo.",
				Causas: []string{
					"Nome da instancia digitado errado",
					"Instancia nao existe para este cliente",
					"Internet do cliente caida (impede chegar no Vuca)",
				},
				Acoes: []string{
					"Corrija o nome da instancia no formulario do diagnostico",
					"Verifique se outros testes basicos (DNS, IP publico) funcionaram",
					"Se a internet esta OK e a instancia esta certa, acione o time Vuca",
				},
			},
		},
	},

	// =====================================================================
	// RABBITMQ
	// =====================================================================
	"rabbitmq-porta": {
		OQueSignifica: "Testa se o servidor de filas RabbitMQ (que processa jobs de impressao na nuvem Vuca) esta acessivel pelo PC. Se essa porta estiver bloqueada, impressoes via cloud nao chegam.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Porta do RabbitMQ acessivel — o canal de comunicacao para impressao via nuvem esta aberto.",
			},
			StatusFail: {
				Interpretacao: "Porta do RabbitMQ bloqueada. Impressao via cloud Vuca nao vai funcionar deste local.",
				Causas: []string{
					"Firewall do cliente bloqueando saida em porta 5672 ou 15672",
					"Proxy/filtro de rede corporativa restringindo trafego nao-HTTP",
					"Politica de seguranca da operadora bloqueando portas nao-padrao",
				},
				Acoes: []string{
					"Verifique o firewall do PC: porta 5672 saindo deve estar liberada",
					"Em redes corporativas: libere com o time de TI as portas 5672 e 15672 para vucaprint-3.vucasolution.com.br",
					"Algumas operadoras bloqueiam portas alternativas — pode ser necessario contato",
				},
			},
		},
	},

	"rabbitmq-amqp": {
		OQueSignifica: "Alem de testar a porta TCP, valida que o servico na porta 5672 realmente fala o protocolo AMQP do RabbitMQ. Pega o caso raro de porta aberta mas servico errado ocupando aquela porta.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Servidor respondeu corretamente ao handshake AMQP — e' um RabbitMQ saudavel.",
			},
			StatusWarn: {
				Interpretacao: "Servidor AMQP esta na porta mas a versao do protocolo nao bate exatamente. Pode causar incompatibilidades em algumas operacoes.",
				Causas: []string{
					"RabbitMQ desatualizado ou em uma versao antiga",
					"Configuracao customizada de protocolo no servidor",
				},
				Acoes: []string{
					"Reporte ao time Vuca a versao reportada na mensagem para investigarem",
				},
			},
			StatusFail: {
				Interpretacao: "TCP conecta mas o servico nao parece ser AMQP — algum outro programa esta ocupando a porta 5672, ou a porta foi sequestrada por proxy/firewall.",
				Causas: []string{
					"Proxy transparente interceptando a conexao",
					"Outro servico mal configurado escutando na porta 5672",
					"Conexao sendo redirecionada por DNS hijack ou MITM",
				},
				Acoes: []string{
					"Confirme que o trafego para vucaprint-3.vucasolution.com.br:5672 nao esta sendo interceptado",
					"Em redes corporativas, peca ao time de TI para liberar passagem direta sem inspecao",
				},
			},
		},
	},

	// =====================================================================
	// VUCALOCAL
	// =====================================================================
	"vucalocal": {
		OQueSignifica: "Testa se o servico VucaLocal — a aplicacao local instalada na maquina do cliente — esta respondendo na URL informada. Este servico processa pedidos localmente antes de subir para a nuvem.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "VucaLocal respondendo normalmente na URL informada.",
			},
			StatusInfo: {
				Interpretacao: "URL do VucaLocal nao informada — teste pulado. Se o cliente usa VucaLocal, informe a URL local (geralmente http://localhost:8080) e rode de novo.",
			},
			StatusWarn: {
				Interpretacao: "VucaLocal respondeu, mas com status inesperado. Pode estar com problema interno ou a URL informada nao e' do VucaLocal mesmo.",
				Causas: []string{
					"VucaLocal subiu mas com erro de inicializacao",
					"URL informada esta errada (apontando para outro servico)",
					"Servico subindo no momento do teste",
				},
				Acoes: []string{
					"Confirme a URL correta do VucaLocal (geralmente http://localhost:8080)",
					"Reinicie o servico VucaLocal e tente novamente",
					"Verifique os logs do VucaLocal para detalhes do erro interno",
				},
			},
			StatusFail: {
				Interpretacao: "VucaLocal nao esta respondendo. Pedidos nao serao processados localmente — em caso de queda da internet, a operacao vai parar.",
				Causas: []string{
					"Servico VucaLocal nao esta rodando (parado ou nunca foi iniciado)",
					"URL informada esta errada (ex: porta diferente)",
					"VucaLocal travou (precisa reiniciar)",
					"URL publica colocada por engano em vez da URL local (http://localhost:PORTA)",
				},
				Acoes: []string{
					"Confirme que o servico VucaLocal esta instalado e rodando na maquina",
					"Verifique a porta correta (geralmente 8080) e atualize o formulario",
					"Reinicie o VucaLocal se estiver travado",
					"Se a URL for publica (com vucasolution.com.br), nao e' do VucaLocal — VucaLocal e' LOCAL",
				},
			},
		},
	},

	// =====================================================================
	// IMPRESSORAS
	// =====================================================================
	"impressora-tcp": {
		OQueSignifica: "Verifica se a impressora termica esta acessivel pela rede da loja, respondendo na porta padrao de impressao (9100). Este e' o teste basico de conectividade — confirma que existe algo respondendo no endereco.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Impressora acessivel pela rede — algo esta respondendo no IP e porta informados.",
			},
			StatusFail: {
				Interpretacao: "Impressora nao esta respondendo. Ela esta offline, com problema, ou o IP informado esta errado.",
				Causas: []string{
					"Impressora desligada ou sem energia",
					"Cabo de rede desconectado ou Wi-Fi caiu (no caso de impressora wireless)",
					"IP da impressora mudou (esta com DHCP em vez de IP fixo)",
					"Firewall do PC ou da rede bloqueando porta 9100",
					"Impressora travada (papel atolado, tampa aberta, em estado de erro)",
				},
				Acoes: []string{
					"Confirme que a impressora esta ligada e com luz verde",
					"Imprima o teste de configuracao da impressora (geralmente apertar 2 botoes juntos enquanto liga)",
					"Compare o IP impresso no teste com o IP informado aqui no diagnostico",
					"Configure IP fixo na impressora se ela esta com DHCP (sempre que possivel)",
					"Verifique cabo de rede ou sinal Wi-Fi da impressora",
				},
			},
		},
	},

	"impressora-esc": {
		OQueSignifica: "Alem de testar a porta TCP, envia um comando ESC/POS (status request) e verifica se a impressora responde corretamente. Pega o caso de impressora 'viva' na rede mas que nao consegue mais imprimir (travada, sem papel, tampa aberta).",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Impressora respondeu ao comando ESC/POS. Esta totalmente operacional ou reportou status especifico (tampa aberta, papel acabando) na mensagem.",
			},
			StatusWarn: {
				Interpretacao: "Impressora aceita TCP mas nao respondeu ao comando ESC/POS de status. Pode ser modelo limitado que nao suporta status em tempo real, ou impressora travada.",
				Causas: []string{
					"Modelo de impressora antigo/generico sem suporte a real-time status",
					"Impressora USB-emulada (gateway de impressao USB-para-TCP) que nao repassa comandos",
					"Impressora travada num estado de erro que nao responde",
				},
				Acoes: []string{
					"Se a impressora estiver imprimindo normalmente apesar desse aviso, ignore",
					"Se nao estiver imprimindo, reinicie ela (tira e religa)",
					"Em caso de USB-emulada, este teste e' inconclusivo — confirme imprimindo um teste",
				},
			},
			StatusFail: {
				Interpretacao: "TCP fechado — impressora totalmente fora do ar. Mesma causa do teste TCP basico.",
				Causas: []string{
					"Impressora desligada",
					"Sem conexao de rede",
					"IP errado no formulario",
				},
				Acoes: []string{
					"Veja as acoes sugeridas no teste de TCP da mesma impressora acima",
				},
			},
		},
	},

	// =====================================================================
	// PORTAS CUSTOMIZADAS
	// =====================================================================
	"porta-customizada": {
		OQueSignifica: "Teste TCP customizado adicionado pelo tecnico no formulario. Usado para validar conectividade com servicos externos a esta lista padrao — bancos de dados, APIs internas, integracoes.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Porta acessivel. O servico esta respondendo no endereco informado.",
			},
			StatusFail: {
				Interpretacao: "Porta bloqueada ou servico nao esta respondendo.",
				Causas: []string{
					"Servico de destino esta parado",
					"Firewall (no destino ou no PC) bloqueando a porta",
					"IP/hostname informado esta errado",
					"Servico mudou de porta",
				},
				Acoes: []string{
					"Confirme com o responsavel pelo servico se ele esta no ar",
					"Confirme o IP e a porta corretos",
					"Verifique se ha bloqueio de firewall no caminho",
				},
			},
		},
	},

	// =====================================================================
	// GENERICO (fallback)
	// =====================================================================
	"generico": {
		OQueSignifica: "Verificacao especifica do diagnostico. Veja a mensagem tecnica para detalhes.",
		Variantes: map[Status]variantePorStatus{
			StatusOK: {
				Interpretacao: "Verificacao passou.",
			},
			StatusWarn: {
				Interpretacao: "Verificacao passou com aviso.",
				Acoes:         []string{"Revise a mensagem tecnica para entender o que requer atencao."},
			},
			StatusFail: {
				Interpretacao: "Verificacao falhou.",
				Acoes:         []string{"Investigue a falha apontada na mensagem tecnica."},
			},
			StatusInfo: {
				Interpretacao: "Verificacao informativa — sem julgamento de sucesso ou falha.",
			},
		},
	},
}
