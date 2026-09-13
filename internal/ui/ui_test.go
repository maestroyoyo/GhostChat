package ui

// TestCrearSalaIniciaRed: regresión del fallo principal v1. Al pulsar
// "Crear Sala Blindada" la aplicación solo generaba el código y nunca
// iniciaba la red; este test verifica que ahora el creador se conecta
// (emite el mensaje de arranque de la red en IncomingMessages).

import (
	"strings"
	"testing"
	"time"

	"github.com/maestroyoyo/ghostchat/internal/network"

	tea "github.com/charmbracelet/bubbletea"
)

func TestCrearSalaIniciaRed(t *testing.T) {
	m := initialModel()
	// Enter en el menú con el cursor en la opción 0 ("Crear Sala Blindada").
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	deadline := time.After(30 * time.Second)
	for {
		select {
		case msg := <-network.IncomingMessages:
			if strings.Contains(msg, "Conectando a la infraestructura") {
				network.DestroySession()
				return // PASS: el creador inició la red
			}
			t.Logf("mensaje recibido: %q", msg)
		case <-deadline:
			network.DestroySession()
			t.Fatal("FALLO RAÍZ PERSISTENTE: al crear sala no se inició la red (no llegó el mensaje de conexión)")
		}
	}
}
