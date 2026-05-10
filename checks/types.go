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
}

// Config recebido do formulario.
type Config struct {
	Instancia string       `json:"instancia"`
	RabbitMQ  RabbitConfig `json:"rabbitmq"`
	VucaLocal string       `json:"vucalocal"`
	Impressoras []Impressora `json:"impressoras"`
}

type RabbitConfig struct {
	Host   string `json:"host"`
	Portas []int  `json:"portas"`
}

type Impressora struct {
	Nome  string `json:"nome"`
	IP    string `json:"ip"`
	Porta int    `json:"porta"`
}

// Relatorio agregado.
type Relatorio struct {
	GeradoEm   time.Time   `json:"gerado_em"`
	Config     Config      `json:"config"`
	Resultados []Resultado `json:"resultados"`
}
