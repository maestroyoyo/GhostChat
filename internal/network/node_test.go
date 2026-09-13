package network

// Pruebas de diagnóstico de la versión V1 (antes de las correcciones).
// Documentan los dos fallos principales reprodudicibles:
//   TestCreadorNoIniciaRed : el flujo "Crear Sala Blindada" nunca inicia la red libp2p.
//   TestFramingV1          : sin encuadre de longitud, una ráfaga de mensajes llega
//                            concatenada y el descifrado AES-GCM falla ("paquete corrupto").

import (
	"bufio"
	"context"
	"testing"
	"time"

	"github.com/maestroyoyo/ghostchat/internal/crypto"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// resetNetworkTest limpia el estado global del paquete entre pruebas.
func resetNetworkTest(t *testing.T) {
	t.Helper()
	if globalHost != nil {
		globalHost.Close()
		globalHost = nil
	}
	roomKey = nil
	connectedPeers = make(map[peer.ID]network.Stream)
	for {
		select {
		case <-IncomingMessages:
		default:
			return
		}
	}
}

// writeFull escribe todos los bytes, gestionando escrituras parciales.
func writeFull(t *testing.T, s network.Stream, b []byte) {
	t.Helper()
	for len(b) > 0 {
		n, err := s.Write(b)
		if err != nil {
			t.Fatalf("error escribiendo en el stream: %v", err)
		}
		b = b[n:]
	}
}

// assertReceived espera un mensaje concreto en el canal con timeout.
func assertReceived(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("mensaje recibido = %q, esperado %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout esperando el mensaje %q", want)
	}
}

// TestCreadorNoIniciaRed reproduce el flujo exacto del case 0 de terminal.go (v1):
// al pulsar "Crear Sala Blindada" solo se genera la invitación y se muestra;
// nunca se llama a ConnectToRoomWithInvite, por lo que la red queda muerta:
// roomKey == nil, globalHost == nil y SendMessage es inerte.
func TestCreadorNoIniciaRed(t *testing.T) {
	resetNetworkTest(t)

	invite := GenerateSecureInvite()
	if len(invite) != 32 {
		t.Fatalf("código de invitación con longitud %d, se esperaba 32 (16 bytes hex)", len(invite))
	}
	// Nota: en v1 la UI solo muestra el código y pasa a chatState.
	_ = invite

	if roomKey != nil {
		t.Error("FALLO RAÍZ v1: después de solo generar la invitación roomKey debería ser nil (el creador nunca deriva la clave)")
	}
	if globalHost != nil {
		t.Error("FALLO RAÍZ v1: después de solo generar la invitación globalHost debería ser nil (el creador nunca inicia el host libp2p)")
	}
	if len(connectedPeers) != 0 {
		t.Error("no debería haber peers conectados sin red iniciada")
	}

	before := len(IncomingMessages)
	SendMessage("hola") // en v1 no debe hacer nada (roomKey == nil) ni panickear
	time.Sleep(200 * time.Millisecond)
	if len(IncomingMessages) != before {
		t.Error("SendMessage no debería producir salida con la red inactiva")
	}
}

// TestFramingV1 demuestra que el protocolo v1 (bufio.Read de hasta 4096 bytes
// sin delimitadores) no puede separar una ráfaga de mensajes cifrados: el
// descifrado AES-GCM falla y la app reporta "Paquete corrupto".
// El handler replica readData v1 (node.go:103-120).
func TestFramingV1(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	key := crypto.DeriveKey("tema-prueba-v1")
	got := make(chan string, 10)
	corrupt := make(chan struct{}, 10)

	v1Handler := func(s network.Stream) {
		// Replica EXACTA del readData v1
		reader := bufio.NewReader(s)
		for {
			buf := make([]byte, 4096)
			n, err := reader.Read(buf)
			if err != nil {
				return
			}
			decrypted, err := crypto.Decrypt(buf[:n], key)
			if err != nil {
				corrupt <- struct{}{}
				continue
			}
			got <- string(decrypted)
		}
	}

	hB, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("creando host receptor: %v", err)
	}
	defer hB.Close()
	hB.SetStreamHandler(protocolID, v1Handler)

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

	// 1) Mensaje único aislado -> debe descifrarse correctamente.
	blob1, err := crypto.Encrypt([]byte("mensaje unico"), key)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, stream, blob1)
	assertReceived(t, got, "mensaje unico")

	// 2) Ráfaga de 3 mensajes enviados de golpe (coalescencia TCP típica):
	//    llegan concatenados sin delimitadores y el descifrado falla.
	blob2, _ := crypto.Encrypt([]byte("segundo"), key)
	blob3, _ := crypto.Encrypt([]byte("tercero"), key)
	blob4, _ := crypto.Encrypt([]byte("cuarto"), key)
	burst := append(append([]byte{}, blob2...), blob3...)
	burst = append(burst, blob4...)
	writeFull(t, stream, burst)

	select {
	case <-corrupt:
		// Defecto v1 confirmado: los mensajes en ráfaga se corrompen y se pierden.
		t.Log("DEFECTO v1 CONFIRMADO: ráfaga de mensajes -> 'Paquete corrupto' (mensajes perdidos)")
	case <-time.After(5 * time.Second):
		t.Fatal("se esperaba corrupción con el encuadre v1, pero la ráfaga llegó intacta (revisar suposición)")
	}
}
