package xray

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// Xray encodes REALITY keys as unpadded URL-safe base64 of the 32-byte X25519 key.
var keyEnc = base64.RawURLEncoding

// GenerateRealityKeys returns a fresh X25519 key pair in Xray's encoding.
func GenerateRealityKeys() (privateKey, publicKey string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return keyEnc.EncodeToString(k.Bytes()), keyEnc.EncodeToString(k.PublicKey().Bytes()), nil
}

// PublicFromPrivate derives the REALITY public key from a private key.
func PublicFromPrivate(privateKey string) (string, error) {
	raw, err := keyEnc.DecodeString(privateKey)
	if err != nil || len(raw) != 32 {
		return "", errors.New("reality private key must be 32 bytes in unpadded base64url")
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return keyEnc.EncodeToString(k.PublicKey().Bytes()), nil
}

// NewShortID returns a random 8-byte (16 hex chars) REALITY short id.
func NewShortID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
