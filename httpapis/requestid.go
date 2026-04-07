package httpapis

import (
	"crypto/rand"
	"encoding/hex"
)

func cryptoRandRead(b []byte) (int, error) { return rand.Read(b) }
func hexEncode(b []byte) string            { return hex.EncodeToString(b) }
