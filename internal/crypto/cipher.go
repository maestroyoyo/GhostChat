package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

const (
	messageKeyContext = "ghostchat/v3/message-key"
	discoveryContext  = "ghostchat/v3/discovery"
)

func DeriveKey(token string) []byte {
	return derive(token, messageKeyContext)
}

// DeriveDiscoveryTopic devuelve un espacio de nombres de rendezvous opaco e
// independiente criptográficamente de la clave de cifrado. RoutingDiscovery
// vuelve a aplicar SHA-256 antes de publicarlo como CID en la DHT.
func DeriveDiscoveryTopic(token string) string {
	return hex.EncodeToString(derive(token, discoveryContext))
}

func derive(token, context string) []byte {
	key, err := hkdf.Key(sha256.New, []byte(token), nil, context, 32)
	if err != nil {
		// Una expansión HKDF-SHA-256 de 32 bytes nunca supera el límite de RFC 5869.
		panic("ghostchat: parámetros HKDF no válidos: " + err.Error())
	}
	return key
}

func Encrypt(plaintext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aesgcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	ciphertext := aesgcm.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

func Decrypt(ciphertext []byte, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesgcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("mensaje interceptado o corrupto")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]

	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}
