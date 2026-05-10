package checks

import (
	"fmt"
	"runtime"
	"time"
)

// CheckSistema retorna informacoes basicas do sistema.
func CheckSistema() Resultado {
	r := Resultado{Categoria: "Sistema", Nome: "Sistema operacional"}
	zona, offset := time.Now().Zone()
	r.Detalhes = map[string]interface{}{
		"so":          runtime.GOOS,
		"arquitetura": runtime.GOARCH,
		"cpus":        runtime.NumCPU(),
		"timezone":    zona,
		"offset_seg":  offset,
		"hora_local":  time.Now().Format("2006-01-02 15:04:05"),
	}
	r.Status = StatusInfo
	r.Mensagem = fmt.Sprintf("%s/%s — %d CPUs — %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), zona)
	return r
}
