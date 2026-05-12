package checks

import "time"

type Status string

const (
	StatusOK    Status = "ok"
	StatusWarn  Status = "warn"
	StatusFail  Status = "fail"
	StatusInfo  Status = "info"
)

// Resultado representa o resultado de um check individual.
type Resultado struct {
	Categoria string                 `json:"categoria"`
	Nome      string                 `json:"nome"`
	Status    Status                 `json:"status"`
	Mensagem  string                 `json:"mensagem"`
	Detalhes  map[string]interface{} `json:"detalhes,omitempty"`
	DuracaoMs int64                  `json:"duracao_ms"`
	SubPassos []SubPasso             `json:"subpassos,omitempty"`
}

// SubPasso representa uma etapa interna dentro de um Check. Util para o
// relatorio detalhado (aba "Etapas") e o acompanhamento ao vivo.
type SubPasso struct {
	Descricao string `json:"descricao"`
	Status    Status `json:"status"`
	DuracaoMs int64  `json:"duracao_ms"`
	Detalhe   string `json:"detalhe,omitempty"`
}

// Evento representa qualquer mensagem trafegada no stream NDJSON do
// servidor para o cliente. Tipo determina a forma de Dados:
//   - "check_inicio" -> Resultado (so categoria/nome — placeholder)
//   - "subpasso"     -> SubPassoEvento
//   - "resultado"    -> Resultado
//   - "final"        -> Relatorio
type Evento struct {
	Tipo  string      `json:"tipo"`
	Dados interface{} `json:"dados"`
}

// SubPassoEvento e' o payload de um evento "subpasso" no stream. Carrega
// metadata identificando a qual check o sub-passo pertence.
type SubPassoEvento struct {
	CheckCategoria string   `json:"check_categoria"`
	CheckNome      string   `json:"check_nome"`
	SubPasso       SubPasso `json:"subpasso"`
}

// Config recebido do formulario.
type Config struct {
	Instancia          string       `json:"instancia"`
	RabbitMQ           RabbitConfig `json:"rabbitmq"`
	VucaLocal          string       `json:"vucalocal"`
	Impressoras        []Impressora `json:"impressoras"`
	PortasCustomizadas []TestePorta `json:"portas_customizadas,omitempty"`
}

type RabbitConfig struct {
	Host    string `json:"host"`
	Portas  []int  `json:"portas"`
	Usuario string `json:"usuario,omitempty"`
	Senha   string `json:"senha,omitempty"`
}

type Impressora struct {
	Nome         string `json:"nome"`
	IP           string `json:"ip"`
	Porta        int    `json:"porta"`
	UnidadeID    string `json:"unidade_id,omitempty"`
	ImpressoraID string `json:"impressora_id,omitempty"`
}

// TestePorta e' uma entrada de teste TCP arbitrario (host:porta com um nome
// legivel). Usado pra validar qualquer servico extra que o tecnico queira
// confirmar no ambiente do cliente.
type TestePorta struct {
	Nome  string `json:"nome"`
	Host  string `json:"host"`
	Porta int    `json:"porta"`
}

// Relatorio agregado.
type Relatorio struct {
	GeradoEm   time.Time   `json:"gerado_em"`
	Config     Config      `json:"config"`
	Resultados []Resultado `json:"resultados"`
}
