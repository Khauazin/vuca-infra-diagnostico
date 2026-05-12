# vuca-infra-diagnostico

Ferramenta CLI/Web para validar a infraestrutura de rede de estabelecimentos antes da implantação (ou durante o suporte) do sistema Vuca. Executa em sequência um conjunto extenso de checks que simulam o que a aplicação real faz (DNS, HTTPS, AMQP, ESC/POS, etc.), classifica cada resultado em `OK` / `Atenção` / `Falha` / `Info`, e gera um relatório HTML auto-contido com explicações didáticas, causas prováveis e ações sugeridas para cada problema.

A ferramenta é distribuída como um único `.exe` Windows (ou binário macOS) sem dependências externas — o técnico de campo copia, executa, e o navegador abre automaticamente em `http://localhost:7777`.

---

## Sumário

1. [Arquitetura](#arquitetura)
2. [Stack técnica](#stack-técnica)
3. [Checks disponíveis](#checks-disponíveis)
4. [Setup e build](#setup-e-build)
5. [Estrutura do projeto](#estrutura-do-projeto)
6. [Protocolo de streaming NDJSON](#protocolo-de-streaming-ndjson)
7. [Como adicionar um check novo](#como-adicionar-um-check-novo)
8. [Catálogo de explicações didáticas](#catálogo-de-explicações-didáticas)
9. [Decisões técnicas](#decisões-técnicas)
10. [Troubleshooting](#troubleshooting)

---

## Arquitetura

```
                                ┌──────────────────────────┐
                                │  Navegador (Chrome/Edge) │
                                │  - Formulário (HTML)     │
                                │  - Streaming live (JS)   │
                                └──────────┬───────────────┘
                                           │ HTTP localhost:7777
                                           │
┌──────────────────────────────────────────▼─────────────────────────────┐
│  Binário Go (vuca-infra-diagnostico.exe)                               │
│                                                                         │
│  ┌─────────────────────────┐    ┌────────────────────────────────┐    │
│  │  server/server.go       │    │  web/ (embed.FS)               │    │
│  │  - net/http handlers    │◀───│  - index.html (form + live UI) │    │
│  │  - NDJSON streaming     │    │  - app.js (renderer + streaming)│   │
│  │  - relatorio template   │    │  - style.css                   │    │
│  └──────────┬──────────────┘    │  - relatorio.html (Go template)│    │
│             │                   └────────────────────────────────┘    │
│             ▼                                                          │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │  checks/                                                       │    │
│  │  - runner.go: orquestrador (executar checks em sequência)     │    │
│  │  - sistema.go, conectividade.go, rede_local.go, ambiente.go,  │    │
│  │    portas.go, impressoras.go, tls_cert.go, vuca_protocolos.go │    │
│  │  - explicacoes.go: catálogo de causas/ações didáticas         │    │
│  │  - types.go: Resultado, SubPasso, Evento, Status              │    │
│  └────────────────┬─────────────────────────────────────────────┘    │
└───────────────────┼────────────────────────────────────────────────────┘
                    │
                    ▼
        ┌──────────────────────────┐  ┌──────────────────┐  ┌─────────────┐
        │  Internet (Cloudflare,   │  │  Rede interna    │  │  Sistema    │
        │  vucasolution.com.br)    │  │  (impressoras,   │  │  operacional│
        │  DNS, HTTPS, AMQP        │  │  VucaLocal)      │  │  (ipconfig) │
        └──────────────────────────┘  └──────────────────┘  └─────────────┘
```

Fluxo de uma execução:

1. Usuário preenche formulário no navegador e clica em "Executar diagnóstico"
2. JS faz `POST /api/diagnosticar` com a config em JSON
3. Servidor inicia goroutine que chama `checks.Executar(cfg, eventosCh)`
4. Para cada check, o runner envia 1 evento `check_inicio`, N eventos `subpasso` durante a execução, e 1 evento `resultado` no fim
5. Servidor encaminha cada evento via `application/x-ndjson` (1 JSON por linha) com `Flush()` imediato
6. JS lê o stream com `ReadableStream` e atualiza a UI em tempo real
7. Ao terminar, servidor envia evento `final` com o `Relatorio` consolidado
8. JS habilita botão "Baixar HTML" que faz `POST /api/relatorio.html` retornando o template Go renderizado

---

## Stack técnica

- **Linguagem**: Go 1.22+
- **Dependências externas**: zero (apenas standard library)
- **Frontend**: HTML/CSS/JS vanilla, sem build step, sem libs
- **Distribuição**: binário único compilado estaticamente, web assets via `embed.FS`
- **Protocolos**:
  - HTTPS para checks externos
  - TCP raw para `CheckPortaTCP`, AMQP, ESC/POS
  - DNS via `net.Resolver` (sistema + customizado pra `1.1.1.1` e `8.8.8.8`)
  - HTTP/1.1 com `httptrace` para medir fases (DNS/TCP/TLS/TTFB)
- **OS suportado**:
  - Windows (alvo principal — `ipconfig`, `powercfg`, `netsh`, `ping`)
  - macOS (`go run` funciona, mas alguns checks `Ambiente` reportam INFO por não estarem implementados)

---

## Checks disponíveis

Total: **24 tipos distintos**, organizados em 8 categorias.

### Sistema (1 check, sempre roda primeiro)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckSistema` | Coleta SO, arquitetura, CPUs, fuso horário, hora local | `INFO` |

### Rede Local (6 checks, sempre rodam)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckRedeLocalInfo` | Lista interfaces ativas com IP/máscara/CIDR | `INFO`, `WARN` |
| `CheckGatewayLocal` | Identifica o gateway padrão (Windows: parse `ipconfig`) e mede latência em TCP/53,80,443 | `OK`, `WARN`, `INFO` |
| `CheckMTU` | Descobre MTU efetivo via `ping -f -l <N>`; testa 1500, 1400, 1300, 1200, 1100, 1000 | `OK`, `WARN`, `INFO` |
| `CheckIPPublico` | Consulta `cloudflare.com/cdn-cgi/trace` para extrair IP público, datacenter (`colo`), país (`loc`) | `INFO`, `WARN` |
| `CheckBanda` | Mede throughput baixando 10MB de `speed.cloudflare.com/__down?bytes=10000000` | `OK` (≥10Mbps), `WARN` (3-10), `FAIL` (<3) |
| `CheckLatenciaLonga` | 50 amostras TCP em 30s no host da instância; calcula jitter (stddev) e perda | `OK`, `WARN`, `FAIL`, `INFO` |

### Ambiente (3 checks, sempre rodam)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckProxyAtivo` | Detecta `HTTP_PROXY` / `HTTPS_PROXY` em env + `netsh winhttp show proxy` | `OK`, `WARN` |
| `CheckRelogio` | Compara `time.Now()` com `Date` HTTP header de `cloudflare.com` | `OK` (<5s), `WARN` (5-30s), `FAIL` (>30s), `INFO` |
| `CheckPerfilEnergia` | `powercfg /getactivescheme`; WARN se "Power saver" / "Economia" | `INFO`, `WARN` |

### Conectividade (6 checks, dependem de instância)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckDNS` | (1) Resolve `cloudflare.com` para validar DNS local. (2) Resolve `{instancia}.vucasolution.com.br`. (3) Detecta wildcard via subdomínio canário. (4) Multi-servidor (sistema vs 1.1.1.1 vs 8.8.8.8). (5) IPv4 (A) vs IPv6 (AAAA) + tenta TCP/443 no v6. (6) Estabilidade (3 lookups com 200ms entre, compara). (7) Lista DNS servers do SO. | `OK`, `WARN`, `FAIL` |
| `CheckHTTPS` | GET na URL da instância. Lê 2KB do body. Classifica: 404+"default backend" → `FAIL`, outros 4xx → `WARN`, 5xx → `WARN`, 2xx/3xx → `OK`. Este é o **gate** — se `FAIL`, aborta o resto. | `OK`, `WARN`, `FAIL` |
| `CheckLatencia` | 10 amostras TCP em `host:443`. Calcula min/avg/max e perda | `OK`, `WARN`, `FAIL` |
| `CheckHTTPSFases` | GET com `httptrace.ClientTrace` medindo separadamente DNS, TCP, TLS, TTFB. Thresholds: DNS 500/2000, TCP 500/3000, TLS 1000/5000, TTFB 2000/10000 | `OK`, `WARN`, `FAIL` |
| `CheckTLS` | `tls.Dial`, inspeciona `ConnectionState`. Avalia validade do cert (`classificarValidadeCert`), versão TLS, cipher, cadeia. Detecta AV interceptando via `detectarAntivirusMITM` (Bitdefender, Kaspersky, ESET, etc.) | `OK`, `WARN`, `FAIL` |
| `CheckHTTPSConsistencia` | 3 GETs sequenciais com 1s de intervalo. Compara status codes, tempos (variação >3x = WARN), e detecta 200-com-erro no body (palavras: `error`, `failed`, `exception`) | `OK`, `WARN` |

### Validação (sentinelas — emitidos pelo runner)

| Sentinela | Quando aparece | Status |
|---|---|---|
| `Validacao parcial` | Instância vazia — modo "só rede interna" | `WARN` |
| `Diagnostico interrompido` | HTTPS deu `FAIL` — restante foi pulado | `FAIL` |

### RabbitMQ (2 checks por porta configurada)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckPortaTCP` | TCP connect simples em `host:porta` | `OK`, `FAIL` |
| `CheckRabbitMQAMQP` | (Só porta 5672) TCP + envia header AMQP 0-9-1 + lê resposta. Espera frame `Connection.Start` (class=10, method=10). Detecta versão incompatível ou serviço não-AMQP ocupando a porta | `OK`, `WARN`, `FAIL` |

### VucaLocal (1 check)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckVucaLocal` | GET na URL local informada. Mesma classificação do CheckHTTPS (default backend, 4xx/5xx, 2xx). Pula se URL vazia | `OK`, `WARN`, `FAIL`, `INFO` |

### Impressoras (2 checks por impressora)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckImpressora` | TCP connect em `IP:9100` (ou porta configurada) | `OK`, `FAIL` |
| `CheckImpressoraESC` | TCP + envia DLE EOT 1 (`0x10 0x04 0x01`) + lê 1 byte de status. Interpreta bits (online/offline, tampa, papel). **Não imprime nada** | `OK`, `WARN`, `FAIL` |

### Portas customizadas (1 check por entrada)

| Check | Descrição | Status possíveis |
|---|---|---|
| `CheckPortaTCP` reutilizado | TCP connect em qualquer host:porta informado | `OK`, `FAIL` |

---

## Setup e build

### Pré-requisitos

- **Go 1.22+** instalado e no PATH
- Windows: `ipconfig`, `netsh`, `ping`, `powercfg` (presentes no SO por padrão)

### Rodar em desenvolvimento

```bash
go run .
```

O servidor sobe em `http://127.0.0.1:7777` e abre o navegador automaticamente.

> ⚠️ **Windows com Smart App Control ligado**: o `.exe` gerado em `%LOCALAPPDATA%\go-build` pode ser bloqueado. Use `go build -o nome.exe .` na pasta do projeto e rode `.\nome.exe` direto. Se persistir, adicione a pasta do projeto à exclusão do Defender (Configurações → Privacidade e segurança → Segurança do Windows → Vírus e ameaças → Exclusões).

### Build para distribuição

```bash
# Windows x86_64
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/vuca-infra-diagnostico.exe .

# macOS ARM64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/vuca-infra-diagnostico-mac .

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o dist/vuca-infra-diagnostico-mac-intel .
```

Binário resultante: ~10MB, single-file, sem dependências runtime.

### Verificação de qualidade

```bash
go vet ./...     # análise estática
go build ./...   # build de sanidade
```

---

## Estrutura do projeto

```
.
├── main.go                          # entrypoint: sobe servidor + abre navegador
├── go.mod
├── go.sum
├── README.md
│
├── server/
│   └── server.go                    # HTTP handlers, template funcs, NDJSON streaming
│
├── checks/
│   ├── types.go                     # Resultado, SubPasso, Evento, SubPassoEvento, Status, Config
│   ├── runner.go                    # Executar() orquestra todos os checks
│   ├── sistema.go                   # CheckSistema
│   ├── conectividade.go             # CheckDNS, CheckHTTPS, CheckLatencia, CheckHTTPSFases,
│   │                                # CheckHTTPSConsistencia, helpers (resolverViaServidor, etc.)
│   ├── rede_local.go                # CheckRedeLocalInfo, CheckGatewayLocal, CheckMTU,
│   │                                # CheckIPPublico, CheckBanda, CheckLatenciaLonga
│   ├── ambiente.go                  # CheckProxyAtivo, CheckRelogio, CheckPerfilEnergia
│   ├── portas.go                    # CheckPortaTCP, CheckVucaLocal
│   ├── impressoras.go               # CheckImpressora
│   ├── tls_cert.go                  # CheckTLS, detectarAntivirusMITM, classificarValidadeCert
│   ├── vuca_protocolos.go           # CheckRabbitMQAMQP, CheckImpressoraESC
│   └── explicacoes.go               # ExplicarResultado + catálogo de causas/ações didáticas
│
└── web/
    ├── index.html                   # painel ao vivo (formulário + cards streaming)
    ├── style.css
    ├── app.js                       # streaming reader, render incremental, escapeHtml
    └── relatorio.html               # Go template para o HTML baixado (auto-contido)
```

---

## Protocolo de streaming NDJSON

O endpoint `/api/diagnosticar` retorna `application/x-ndjson; charset=utf-8` com 1 JSON por linha. O servidor faz `Flush()` após cada linha. Eventos possíveis:

### `check_inicio`

Emitido antes de cada check começar.

```json
{
  "tipo": "check_inicio",
  "dados": {
    "categoria": "Conectividade",
    "nome": "Resolucao DNS",
    "status": "info",
    "mensagem": "Executando..."
  }
}
```

### `subpasso`

Emitido durante a execução do check, uma vez por sub-validação.

```json
{
  "tipo": "subpasso",
  "dados": {
    "check_categoria": "Conectividade",
    "check_nome": "Resolucao DNS",
    "subpasso": {
      "descricao": "Resolver dominio externo (cloudflare.com)",
      "status": "ok",
      "duracao_ms": 23,
      "detalhe": "Resolveu para [104.16.132.229]"
    }
  }
}
```

### `resultado`

Emitido ao final de cada check com o `Resultado` consolidado (inclui todos os sub-passos).

```json
{
  "tipo": "resultado",
  "dados": {
    "categoria": "Conectividade",
    "nome": "Resolucao DNS",
    "status": "warn",
    "mensagem": "Aviso: instancia resolveu via wildcard DNS...",
    "detalhes": { ... },
    "duracao_ms": 119,
    "subpassos": [ ... ]
  }
}
```

### `final`

Último evento, contém o `Relatorio` completo (para o frontend persistir e usar no botão de baixar).

```json
{
  "tipo": "final",
  "dados": {
    "gerado_em": "2026-05-12T14:30:00Z",
    "config": { ... },
    "resultados": [ ... ]
  }
}
```

O frontend (`web/app.js`) lê via `ReadableStream` + `TextDecoder`, faz `buffer.split('\n')` para extrair linhas completas, e dispatcha cada evento para a função correspondente (`obterOuCriarCard`, `adicionarSubpassoAoCard`, `finalizarCard`, `atualizarResumo`).

---

## Como adicionar um check novo

1. **Escolha a categoria** (existente ou nova). Categorias correntes: `Sistema`, `Rede Local`, `Ambiente`, `Conectividade`, `RabbitMQ`, `VucaLocal`, `Impressoras`, `Portas customizadas`.

2. **Escreva a função no arquivo apropriado** (ou crie um novo `.go` em `checks/`). Assinatura padrão:

```go
func CheckNovo(parametros..., emit func(SubPasso)) Resultado {
    r := Resultado{Categoria: "MinhaCategoria", Nome: "Nome do check"}
    subpassos := []SubPasso{}
    add := func(sp SubPasso) {
        subpassos = append(subpassos, sp)
        if emit != nil {
            emit(sp)
        }
    }

    inicio := time.Now()

    // ... lógica do check, chamando add(SubPasso{...}) a cada passo ...

    r.SubPassos = subpassos
    r.DuracaoMs = time.Since(inicio).Milliseconds()
    r.Status = StatusOK // ou WARN/FAIL/INFO
    r.Mensagem = "..."
    r.Detalhes = map[string]interface{}{...} // opcional
    return r
}
```

3. **Conecte ao runner** em `checks/runner.go`:

```go
runCheck("MinhaCategoria", "Nome do check", func(emit func(SubPasso)) Resultado {
    return CheckNovo(parametros..., emit)
})
```

4. **Adicione a explicação didática** em `checks/explicacoes.go`:

   - Adicione um caso em `classificarTipoCheck` para mapear categoria/nome → tipo string
   - Adicione a entrada em `explicacoesPorTipo[tipoString]` com `OQueSignifica` e variantes por status (`StatusOK`, `StatusWarn`, `StatusFail`, `StatusInfo` com `Interpretacao`, `Causas`, `Acoes`)

5. **Build e teste**:

```bash
go build ./...
go run .
```

---

## Catálogo de explicações didáticas

O arquivo `checks/explicacoes.go` é o coração da experiência do usuário final. Cada tipo de check tem:

- **`OQueSignifica`**: descrição estática do que o teste valida (igual para todos os status)
- **Para cada `Status` possível**:
  - **`Interpretacao`**: significado humano daquele resultado
  - **`Causas`**: lista de causas prováveis (vazia para `OK`)
  - **`Acoes`**: passos práticos com instruções específicas (ex: "Painel de Controle → Opções de Energia → Alto desempenho")

A função `ExplicarResultado(r Resultado) Explicacao` retorna a `Explicacao` adequada combinando descrição estática + variante por status. Se não houver entrada específica, cai em `fallbackPorStatus` que retorna uma versão genérica.

Tipos catalogados (24): `sistema`, `rede-local-interfaces`, `rede-local-gateway`, `rede-local-mtu`, `rede-local-ip-publico`, `rede-local-banda`, `rede-local-latencia-longa`, `ambiente-proxy`, `ambiente-relogio`, `ambiente-perfil-energia`, `conectividade-dns`, `conectividade-https`, `conectividade-latencia`, `conectividade-fases`, `conectividade-tls`, `conectividade-consistencia`, `validacao-parcial`, `validacao-interrompido`, `rabbitmq-porta`, `rabbitmq-amqp`, `vucalocal`, `impressora-tcp`, `impressora-esc`, `porta-customizada`, `generico` (fallback).

Esta tabela alimenta tanto o painel ao vivo (futuramente, hoje só usa a `Mensagem` técnica) quanto o relatório baixado (renderiza as seções "O que isso significa", "Causas mais comuns", "O que fazer").

---

## Decisões técnicas

### Zero dependências externas

O `go.mod` declara apenas o módulo próprio. Tudo é feito com a stdlib:

- `net/http` para servidor e clientes
- `net/http/httptrace` para medir fases
- `crypto/tls` para validar cert e detectar AV MITM
- `net.Resolver` customizado para multi-server DNS
- `os/exec` para `ipconfig`, `netsh`, `powercfg`, `ping`
- `embed.FS` para servir os assets web sem precisar de pasta externa

### Variáveis de pacote para testabilidade

URLs externas (`urlBanda`, `urlTraceCloudflare`, `urlRelogio`, `dominioRefDNS`) e construtores (`construtorURLInstancia`, `construtorHostInstancia443`) são `var` ao nível de pacote em vez de constantes. Isso permite que testes (em futuras suites) sobrescrevam pra apontar para `httptest.Server` locais sem precisar refatorar assinaturas. Em produção, valores default são os corretos.

### NDJSON em vez de Server-Sent Events ou WebSocket

NDJSON é o formato mais simples possível:

- 1 evento = 1 linha JSON
- `Flush()` após cada linha garante delivery imediato
- Cliente em JS lê com `ReadableStream` + `TextDecoder`
- Sem necessidade de framework no cliente

### Wildcards DNS e gate na HTTPS

A zona `vucasolution.com.br` está atrás de Cloudflare proxy, que aplica wildcard DNS: qualquer subdomínio resolve para os mesmos IPs. Portanto, o DNS sozinho não confirma se uma instância existe — só o HTTPS confirma (cluster responde "default backend - 404" para hostnames não roteados). Por isso o `CheckHTTPS` é o **gate** que aborta o restante dos checks em caso de FAIL.

### Streaming ao vivo com `runCheck`

O runner usa um wrapper `runCheck(categoria, nome, executar)` que:

1. Envia evento `check_inicio` no canal
2. Cria um callback `emit(SubPasso)` que envia eventos `subpasso` durante a execução
3. Chama o check passando o emit
4. Envia evento `resultado` com o `Resultado` final

Isso desacopla a lógica de emissão (que precisa do canal compartilhado) da lógica do check (que só conhece o callback). Permite que cada check seja testável isoladamente passando `nil` no callback.

### embed.FS vs filesystem

Os arquivos `web/*` são embutidos no binário via `//go:embed web` no `main.go`. Isso garante:

- Single-file distribution
- Sem necessidade de copiar pasta extra
- Imutabilidade do conteúdo servido

**Atenção**: em desenvolvimento, mudanças em `web/*` exigem `go build` novamente — o `go run` recompila e re-embeda. Não dá pra editar um arquivo `web/` e dar F5 esperando que apareça (a memória do binário rodando tem a versão antiga).

### Português pt-BR no código

Variáveis, comentários e mensagens em português. O alvo da ferramenta são técnicos de campo brasileiros — código em inglês criava fricção desnecessária na manutenção pela equipe.

---

## Troubleshooting

### "Smart App Control bloqueou este arquivo"

Windows 11 com Smart App Control ativo bloqueia executáveis não-assinados. Soluções:

1. **Adicionar exclusão no Defender** para a pasta do projeto (`Configurações → Privacidade e segurança → Segurança do Windows → Vírus e ameaças → Exclusões`)
2. **Desativar Smart App Control** (drástico — só pode ser reativado reinstalando Windows)
3. **Assinar o binário** com cert de code-signing (não implementado neste projeto)

### `go run .` falha com "fork/exec ...go-build... bloqueado"

Mesma raiz do problema acima — o cache do Go fica em `%LOCALAPPDATA%\go-build` e cria executáveis temporários. Use `go build -o nome.exe .` no diretório do projeto e rode `.\nome.exe` direto.

### `ERR_INVALID_CHUNKED_ENCODING` no console do browser

Bug histórico do projeto (já corrigido). Era causado por race condition no servidor entre a goroutine que drenava o canal e a goroutine que escrevia o evento `final`. Ambas escreviam concorrentemente no `http.ResponseWriter`, corrompendo o chunked encoding. A correção está em `server/server.go:handleDiagnosticar` — agora o canal é drenado na goroutine principal e o check roda em goroutine secundária.

### DNS check sempre dá WARN por wildcard

Comportamento esperado para a zona `vucasolution.com.br` proxy-protegida pelo Cloudflare. Qualquer subdomínio (incluindo nomes inexistentes) resolve para os mesmos IPs do Cloudflare edge. Quem confirma a instância é o `CheckHTTPS`, não o DNS.

### Check ESC/POS dá WARN em impressora que imprime normalmente

Algumas impressoras (modelos antigos, USB-emuladas, ou genéricas) não suportam o comando real-time status request (`DLE EOT 1`). Se a impressora estiver imprimindo na operação normal apesar do WARN, é falso positivo aceitável — o check de TCP da mesma impressora deve estar `OK`.

---

## Licença

Uso interno Vuca Solution.
