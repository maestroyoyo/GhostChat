package network

// Pruebas de regresión de la versión V2 (después de las correcciones):
//   TestFramingV2            : con el encuadre de longitud, una ráfaga de mensajes
//                              y un mensaje largo (>4 KiB, obliga a lecturas
//                              múltiples) llegan completos e intactos.
//   TestCreadorIniciaRedAlConectar : tras llamar a ConnectToRoomWithInvite (lo que
//                              la UI del creador hace ahora), la red está activa.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/maestroyoyo/ghostchat/internal/crypto"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// expectIncoming espera en IncomingMessages un mensaje concreto; falla si aparece
// corrupción de paquete (debería ser imposible con el encuadre v2).
func expectIncoming(t *testing.T, want string) {
	t.Helper()
	for {
		select {
		case msg := <-IncomingMessages:
			if msg == want {
				return
			}
			if strings.Contains(msg, "Paquete corrupto") {
				t.Fatalf("corrupción de paquete con encuadre v2: %q (esperaba %q)", msg, want)
			}
			t.Logf("mensaje intermedio: %q", msg)
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout esperando %q", want)
		}
	}
}

// TestFramingV2 envía una ráfaga de 3 mensajes + 1 mensaje de 5000 bytes usando el
// SendMessage real sobre TCP loopback; el receptor usa el readData v2 real.
func TestFramingV2(t *testing.T) {
	resetNetworkTest(t)
	defer DestroySession()

	roomKey = crypto.DeriveKey("tema-prueba-v2")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("creando host receptor: %v", err)
	}
	defer hB.Close()
	// El receptor usa el readData (v2) real; sin tocar el mapa global para
	// no ensuciar el estado del emisor.
	hB.SetStreamHandler(protocolID, func(s network.Stream) {
		go readData(s)
	})

	hA, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("creando host emisor: %v", err)
	}
	defer hA.Close()

	if err := hA.Connect(ctx, peer.AddrInfo{ID: hB.ID(), Addrs: hB.Addrs()}); err != nil {
		t.Fatalf("conectando hosts: %v", err)
	}
	stream, err := hA.NewStream(ctx, hB.ID(), protocolID)
	if err != nil {
		t.Fatalf("abriendo stream: %v", err)
	}
	defer stream.Close()

	// Simula el descubrimiento de B por parte de A (lo que hace findPeers en el flujo real).
	peersMu.Lock()
	connectedPeers[hB.ID()] = stream
	peersMu.Unlock()

	// Ráfaga: 3 mensajes seguidos de golpe (el caso que rompía el encuadre v1).
	SendMessage("hola")
	SendMessage("segundo")
	SendMessage("tercero")

	// Mensaje largo >4096 bytes: obliga a lecturas múltiples del receptor.
	SendMessage(strings.Repeat("A", 5000))

	expectIncoming(t, "[Anon]: hola")
	expectIncoming(t, "[Anon]: segundo")
	expectIncoming(t, "[Anon]: tercero")
	expectIncoming(t, "[Anon]: "+strings.Repeat("A", 5000))
}

// TestCreadorIniciaRedAlConectar valida el flujo del creador corregido:
// la UI ahora llama a ConnectToRoomWithInvite con el propio código, por lo que
// la red debe quedar activa (clave derivada, host escuchando, envío sin pánico).
func TestCreadorIniciaRedAlConectar(t *testing.T) {
	resetNetworkTest(t)
	defer DestroySession()

	invite := GenerateSecureInvite()
	if err := ConnectToRoomWithInvite(invite); err != nil {
		t.Fatalf("ConnectToRoomWithInvite devolvió error: %v", err)
	}

	if roomKey == nil {
		t.Error("roomKey debería estar derivada tras ConnectToRoomWithInvite")
	}
	if globalHost == nil {
		t.Error("globalHost debería estar inicializado tras ConnectToRoomWithInvite")
	} else if len(globalHost.Addrs()) == 0 {
		t.Error("el host debería estar escuchando (sin direcciones)")
	}

	// Con la red activa, enviar no debe panickear ni quedarse mudo.
	SendMessage("prueba de red activa")
}
