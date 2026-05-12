package checks

import (
	"fmt"
	"runtime"
	"time"
)

// CheckSistema retorna informacoes basicas do sistema.
func CheckSistema(emit func(SubPasso)) Resultado {
	r := Resultado{Categoria: "Sistema", Nome: "Sistema operacional"}
	zona, offset := time.Now().Zone()
	subpassos := []SubPasso{}
	add := func(sp SubPasso) {
		subpassos = append(subpassos, sp)
		if emit != nil {
			emit(sp)
		}
	}

	add(SubPasso{
		Descricao: "Detectar sistema operacional e arquitetura",
		Status:    StatusInfo,
		Detalhe:   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	})
	add(SubPasso{
		Descricao: "Contar CPUs disponiveis",
		Status:    StatusInfo,
		Detalhe:   fmt.Sprintf("%d CPUs", runtime.NumCPU()),
	})
	add(SubPasso{
		Descricao: "Identificar timezone local",
		Status:    StatusInfo,
		Detalhe:   fmt.Sprintf("%s (offset %d segundos)", zona, offset),
	})
	add(SubPasso{
		Descricao: "Capturar hora local",
		Status:    StatusInfo,
		Detalhe:   time.Now().Format("2006-01-02 15:04:05"),
	})
	r.SubPassos = subpassos
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
