package network

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"ghostchat/internal/crypto"

	"github.com/charmbracelet/x/ansi"
	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
)

var (
	IncomingMessages = make(chan string, 100)
	MyNickname       = "Anon"
	globalHost       host.Host
	roomKey          []byte
	connectedPeers   = make(map[peer.ID]network.Stream)
	peersMu          sync.RWMutex
	sessionCtx       context.Context
	sessionCancel    context.CancelFunc
)

const (
	protocolID       = "/ghostchat/2.0.0"
	maxMsgSize       = 64 << 20 // 64 MB: límite de mensaje por seguridad
	discoverInterval = 20 * time.Second
)

// GenerateSecureInvite crea un token de alta entropía
func GenerateSecureInvite() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// ConnectToRoomWithInvite inicia la red, anuncia la sala y la busca de forma continua
func ConnectToRoomWithInvite(inviteCode string) error {
	roomKey = crypto.DeriveKey(inviteCode)

	// Candidatos a relay: los bootstrap públicos de IPFS/libp2p,
	// que suelen correr el servicio de circuito v2. El subsistema
	// AutoRelay prueba cada candidato y descarta los que no relayan.
	// (EnableAutoRelay() sin peer source ni relays estáticos PANICA;
	// por eso se pasa la lista explícita.)
	staticRelays := make([]peer.AddrInfo, 0, len(dht.DefaultBootstrapPeers))
	for _, maddr := range dht.DefaultBootstrapPeers {
		if pi, err := peer.AddrInfoFromP2pAddr(maddr); err == nil {
			staticRelays = append(staticRelays, *pi)
		}
	}

	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/tcp/0"),
		libp2p.NATPortMap(),
		libp2p.EnableRelay(),
		libp2p.EnableAutoRelayWithStaticRelays(staticRelays),
		libp2p.EnableHolePunching(),
	)
	if err != nil {
		return err
	}
	globalHost = h
	peersMu.Lock()
	connectedPeers = make(map[peer.ID]network.Stream)
	peersMu.Unlock()

	// Sesión cancelable: al destruir la sala se detienen todas las goroutines de red
	sessionCtx, sessionCancel = context.WithCancel(context.Background())
	ctx := sessionCtx

	// Configurar el manejador de conexiones entrantes
	globalHost.SetStreamHandler(protocolID, handleStream)

	// Arrancar DHT Global
	kDHT, err := dht.New(globalHost)
	if err != nil {
		h.Close()
		globalHost = nil
		return fmt.Errorf("no se pudo iniciar la DHT: %w", err)
	}
	if err := kDHT.Bootstrap(ctx); err != nil {
		IncomingMessages <- "[!] Arranque DHT parcial: " + err.Error()
	}

	IncomingMessages <- "[*] Conectando a la infraestructura global de IPFS..."
	for _, peerAddr := range dht.DefaultBootstrapPeers {
		pi, err := peer.AddrInfoFromP2pAddr(peerAddr)
		if err != nil {
			continue
		}
		globalHost.Connect(ctx, *pi)
	}

	// Anunciar la sala en la DHT
	routingDiscovery := routing.NewRoutingDiscovery(kDHT)
	util.Advertise(ctx, routingDiscovery, inviteCode)

	go discoverLoop(ctx, routingDiscovery, inviteCode)

	return nil
}

// discoverLoop busca contactos periódicamente hasta que la sesión se destruye.
// En v1 la búsqueda se hacía UNA sola vez; si el contacto se conectaba después,
// nadie le encontraba. Ahora se reintenta cada discoverInterval (20s).
func discoverLoop(ctx context.Context, rd *routing.RoutingDiscovery, topic string) {
	IncomingMessages <- "[*] Buscando contacto en la red oscura..."
	runFindPeers(ctx, rd, topic)
	ticker := time.NewTicker(discoverInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runFindPeers(ctx, rd, topic)
		}
	}
}

func runFindPeers(ctx context.Context, rd *routing.RoutingDiscovery, topic string) {
	peerChan, err := rd.FindPeers(ctx, topic)
	if err != nil {
		return
	}
	for p := range peerChan {
		if p.ID == globalHost.ID() || len(p.Addrs) == 0 {
			continue
		}
		peersMu.RLock()
		_, already := connectedPeers[p.ID]
		peersMu.RUnlock()
		if already {
			continue
		}
		stream, err := globalHost.NewStream(ctx, p.ID, protocolID)
		if err != nil {
			continue
		}
		peersMu.Lock()
		// Doble comprobación tras el dial para evitar streams duplicados
		if _, dup := connectedPeers[p.ID]; dup {
			peersMu.Unlock()
			stream.Close()
			continue
		}
		connectedPeers[p.ID] = stream
		peersMu.Unlock()
		IncomingMessages <- fmt.Sprintf("[+] ¡Conexión segura establecida con %s!", p.ID.String()[:8])
		go readData(stream)
	}
}

// handleStream procesa las conexiones entrantes (con deduplicación)
func handleStream(stream network.Stream) {
	peerID := stream.Conn().RemotePeer()
	peersMu.Lock()
	if _, dup := connectedPeers[peerID]; dup {
		peersMu.Unlock()
		stream.Close()
		return
	}
	connectedPeers[peerID] = stream
	peersMu.Unlock()
	IncomingMessages <- fmt.Sprintf("[+] Contacto entrante detectado: %s", peerID.String()[:8])
	go readData(stream)
}

// writeEncrypted envía un blob cifrado con prefijo de longitud big-endian
// (encuadre v2): permite al receptor separar los mensajes aunque lleguen
// concatenados o fragmentados por la red.
func writeEncrypted(stream network.Stream, data []byte) error {
	if len(data) > maxMsgSize {
		return fmt.Errorf("mensaje de %d bytes supera el máximo de 64 MB", len(data))
	}
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))

	writeAll := func(b []byte) error {
		for len(b) > 0 {
			n, err := stream.Write(b)
			if err != nil {
				return err
			}
			b = b[n:]
		}
		return nil
	}
	if err := writeAll(header); err != nil {
		return err
	}
	return writeAll(data)
}

// readData descifra los mensajes que llegan de la red (encuadre v2 con longitud)
func readData(stream network.Stream) {
	defer stream.Close()
	header := make([]byte, 4)
	for {
		if _, err := io.ReadFull(stream, header); err != nil {
			return
		}
		msgLen := binary.BigEndian.Uint32(header)
		if msgLen > maxMsgSize {
			IncomingMessages <- "[-] Paquete excede el tamaño máximo permitido, descartado."
			return
		}
		buf := make([]byte, msgLen)
		if _, err := io.ReadFull(stream, buf); err != nil {
			return
		}

		decrypted, err := crypto.Decrypt(buf, roomKey)
		if err != nil {
			IncomingMessages <- "[-] Paquete corrupto o clave incorrecta bloqueado."
			continue
		}
		IncomingMessages <- sanitizeRemoteMessage(string(decrypted))
	}
}

// sanitizeRemoteMessage impide que un peer remoto envíe secuencias de control
// al emulador de terminal. Los saltos y tabuladores se convierten en espacios
// para que un mensaje no pueda fabricar líneas adicionales en la interfaz.
func sanitizeRemoteMessage(message string) string {
	message = ansi.Strip(message)
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '\t':
			return ' '
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, message)
}

// SendMessage cifra y difunde el texto a todos los peers conectados
func SendMessage(msg string) {
	if roomKey == nil {
		return
	}
	payload := fmt.Sprintf("[%s]: %s", MyNickname, msg)
	encrypted, err := crypto.Encrypt([]byte(payload), roomKey)
	if err != nil {
		return
	}

	peersMu.RLock()
	streams := make([]network.Stream, 0, len(connectedPeers))
	for _, s := range connectedPeers {
		streams = append(streams, s)
	}
	peersMu.RUnlock()

	for _, stream := range streams {
		writeEncrypted(stream, encrypted)
	}
}

// SendFile procesa y envía archivos cifrados (Versión básica)
func SendFile(filePath string) {
	if roomKey == nil {
		return
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		IncomingMessages <- "[!] Error leyendo el archivo local."
		return
	}

	header := fmt.Sprintf("/file:%s:%x", filepath.Base(filePath), data)
	encrypted, err := crypto.Encrypt([]byte(header), roomKey)
	if err != nil {
		IncomingMessages <- "[!] Error cifrando el archivo."
		return
	}

	peersMu.RLock()
	streams := make([]network.Stream, 0, len(connectedPeers))
	for _, s := range connectedPeers {
		streams = append(streams, s)
	}
	peersMu.RUnlock()

	for _, stream := range streams {
		writeEncrypted(stream, encrypted)
	}
	IncomingMessages <- "[*] Archivo cifrado y enviado con éxito."
}

// DestroySession aplica el Kill Switch
func DestroySession() {
	if sessionCancel != nil {
		sessionCancel()
		sessionCancel = nil
	}
	roomKey = nil
	peersMu.Lock()
	for _, stream := range connectedPeers {
		stream.Close()
	}
	connectedPeers = make(map[peer.ID]network.Stream)
	peersMu.Unlock()
	if globalHost != nil {
		globalHost.Close()
		globalHost = nil
	}
	// Forzar recolección de basura en memoria
	time.Sleep(100 * time.Millisecond)
}
