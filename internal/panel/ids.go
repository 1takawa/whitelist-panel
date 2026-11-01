package panel

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
)

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system RNG failing is unrecoverable
	}
	return b
}

// newID returns a 12-hex-char identifier.
func newID() string { return hex.EncodeToString(randBytes(6)) }

// newUUID returns a random (version 4) UUID.
func newUUID() string {
	b := randBytes(16)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randString(alphabet string, n int) string {
	out := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		out[i] = alphabet[v.Int64()]
	}
	return string(out)
}

const (
	alnum      = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	lowerAlnum = "abcdefghijklmnopqrstuvwxyz0123456789"
)

func newPassword() string { return randString(alnum, 24) }
func newSubID() string    { return randString(lowerAlnum, 16) }
