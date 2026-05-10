# vuca-infra-diagnostico

Ferramenta de diagnostico de infraestrutura para restaurantes que usam o Vuca.

Roda na maquina onde fica o VucaLocal e gera um relatorio HTML com o resultado dos testes:

- **Conectividade**: DNS, HTTPS e latencia/perda de pacote para `{instancia}.vucasolution.com.br`
- **RabbitMQ**: portas TCP (5672, 15672) acessiveis
- **VucaLocal**: endpoint local respondendo
- **Impressoras**: cada impressora cadastrada respondendo na porta de impressao (default 9100)
- **Sistema**: SO, CPU, timezone

## Como usar

1. Tecnico executa `vuca-infra-diagnostico.exe` (Windows) ou `vuca-infra-diagnostico-mac` (Mac)
2. Servidor sobe em `http://localhost:7777` e abre o navegador automaticamente
3. Tecnico preenche:
   - **Instancia** (ex: `lifeboxburger`)
   - URL do **VucaLocal**
   - Host/portas do **RabbitMQ**
   - Lista de **impressoras** (nome, IP, porta)
4. Clica em "Executar diagnostico" — checks rodam em sequencia, com progresso na tela
5. Ao final, clica em "Baixar HTML" para salvar o relatorio (`relatorio-{instancia}-YYYY-MM-DD-HHMM.html`)
6. O HTML e auto-contido (CSS embutido) — pode ser enviado pelo WhatsApp/email para o suporte

A configuracao fica salva no `localStorage` do navegador, entao na proxima execucao o formulario ja vem preenchido.

## Build

Requer Go 1.22+.

```bash
# Local (Mac)
go run .

# Compilar para Windows + Mac
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/vuca-infra-diagnostico.exe .
GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o dist/vuca-infra-diagnostico-mac .
```

Binarios gerados em `dist/` (~9MB cada). Single file — basta copiar e executar, sem dependencias.

## Estrutura

```
checks/        # logica de cada categoria de check
  conectividade.go  # DNS, HTTPS, latencia
  portas.go         # TCP check + VucaLocal
  impressoras.go    # checks de impressoras
  sistema.go        # info do SO
  runner.go         # orquestrador
server/        # servidor HTTP local + handlers
web/           # HTML/CSS/JS do formulario e template do relatorio (embed.FS)
main.go        # entrypoint
```
