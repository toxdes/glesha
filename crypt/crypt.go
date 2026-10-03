package crypt

import (
	"context"
	"crypto"
	_ "crypto/sha512"
	"fmt"
	"io"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
	"github.com/ProtonMail/go-crypto/openpgp/s2k"

	"glesha/file_io"
)

func Config() *packet.Config {
	return &packet.Config{DefaultCipher: packet.CipherAES256, DefaultHash: crypto.SHA512, DefaultCompressionAlgo: packet.CompressionNone, S2KConfig: &s2k.Config{S2KMode: s2k.IteratedSaltedS2K, Hash: crypto.SHA512, S2KCount: 65011712}}
}

type Encryptor interface {
	Encrypt(context.Context, io.Writer, []byte) (io.WriteCloser, error)
}

type Decryptor interface {
	Decrypt(context.Context, io.Reader, []byte) (io.Reader, error)
}

type Cipherer interface {
	Encryptor
	Decryptor
}

type cipher struct{}

func NewCipher() Cipherer { return cipher{} }

func Encrypt(ctx context.Context, w io.Writer, password []byte) (io.WriteCloser, error) {
	return NewCipher().Encrypt(ctx, w, password)
}

func (cipher) Encrypt(ctx context.Context, w io.Writer, password []byte) (io.WriteCloser, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if len(password) == 0 {
		return nil, fmt.Errorf("crypt: empty passphrase")
	}
	return openpgp.SymmetricallyEncrypt(w, password, &openpgp.FileHints{IsBinary: true}, Config())
}
func Decrypt(ctx context.Context, r io.Reader, password []byte) (io.Reader, error) {
	return NewCipher().Decrypt(ctx, r, password)
}

func (cipher) Decrypt(ctx context.Context, r io.Reader, password []byte) (io.Reader, error) {
	tried := false
	m, e := openpgp.ReadMessage(file_io.ContextReader{Ctx: ctx, Reader: r}, nil, func(_ []openpgp.Key, _ bool) ([]byte, error) {
		if tried {
			return nil, fmt.Errorf("crypt: incorrect passphrase")
		}
		tried = true
		return password, nil
	}, Config())
	if e != nil {
		return nil, fmt.Errorf("crypt: decrypt: %w", e)
	}
	return m.UnverifiedBody, nil
}
