package checksum

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash"
)

func Sha256(bytes []byte) []byte {
	h := sha256.Sum256(bytes)
	return h[:]
}

func HexEncodeStr(bytes []byte) string {
	return hex.EncodeToString(bytes)
}

func Md5(bytes []byte) []byte {
	h := md5.Sum(bytes)
	return h[:]
}

func Base64EncodeStr(bytes []byte) string {
	return base64.StdEncoding.EncodeToString(bytes)
}

func Base64DecodeStr(input string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(input)
}

func NewSha256() hash.Hash {
	return sha256.New()
}
