package crypto

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestCIDDescubrimientoNoRevelaClaveMensajes(t *testing.T) {
	invite := "00112233445566778899aabbccddeeff"
	messageKey := DeriveKey(invite)
	discoveryTopic := DeriveDiscoveryTopic(invite)

	if len(messageKey) != 32 {
		t.Fatalf("longitud de clave = %d, se esperaban 32 bytes", len(messageKey))
	}
	if len(discoveryTopic) != 64 {
		t.Fatalf("longitud del tema = %d, se esperaban 64 caracteres hex", len(discoveryTopic))
	}
	if _, err := hex.DecodeString(discoveryTopic); err != nil {
		t.Fatalf("el tema de descubrimiento no es hexadecimal: %v", err)
	}
	if hex.EncodeToString(messageKey) == discoveryTopic {
		t.Fatal("la clave de mensajes y el tema de descubrimiento deben ser independientes")
	}

	// RoutingDiscovery de go-libp2p publica SHA-256(namespace) como digest del
	// multihash dentro del CID público. Esta regresión garantiza que esos bytes
	// públicos nunca vuelvan a ser la clave AES de los mensajes.
	publishedDigest := sha256.Sum256([]byte(discoveryTopic))
	if bytes.Equal(messageKey, publishedDigest[:]) {
		t.Fatal("el digest del CID público revela la clave AES")
	}
}

func TestDominiosDerivadosSonDeterministasYDistintos(t *testing.T) {
	invite := "same invitation"
	if !bytes.Equal(DeriveKey(invite), DeriveKey(invite)) {
		t.Fatal("la derivación de la clave de mensajes no es determinista")
	}
	if DeriveDiscoveryTopic(invite) != DeriveDiscoveryTopic(invite) {
		t.Fatal("la derivación del tema de descubrimiento no es determinista")
	}
	if hex.EncodeToString(DeriveKey(invite)) == DeriveDiscoveryTopic(invite) {
		t.Fatal("los contextos HKDF produjeron la misma salida")
	}
}
