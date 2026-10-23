// Package pushtest is the receive side of Web Push (RFC 8291), written separately from the
// sender so tests check the wire format against an independent implementation. Production
// code does not import it, so it is not part of the binary.
package pushtest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Decrypt opens an aes128gcm body with the subscription's private key and auth secret, as a
// browser does, so a test can read exactly what a push service would hand it.
func Decrypt(body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("push: short body")
	}
	salt, idlen := body[:16], int(body[20])
	if binary.BigEndian.Uint32(body[16:20]) != 4096 || len(body) < 21+idlen {
		return nil, errors.New("push: unexpected record size")
	}
	asPublic, record := body[21:21+idlen], body[21+idlen:]
	as, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		return nil, err
	}
	secret, err := uaPrivate.ECDH(as)
	if err != nil {
		return nil, err
	}
	ikm := hkdfExpand(hkdfExtract(authSecret, secret), append(append([]byte("WebPush: info\x00"), uaPrivate.PublicKey().Bytes()...), asPublic...), 32)
	prk := hkdfExtract(salt, ikm)
	blk, err := aes.NewCipher(hkdfExpand(prk, []byte("Content-Encoding: aes128gcm\x00"), 16))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(blk)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, hkdfExpand(prk, []byte("Content-Encoding: nonce\x00"), 12), record, nil)
	if err != nil {
		return nil, err
	}
	for len(plain) > 0 && plain[len(plain)-1] == 0 {
		plain = plain[:len(plain)-1]
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("push: bad padding")
	}
	return plain[:len(plain)-1], nil
}

func hkdfExtract(salt, ikm []byte) []byte {
	m := hmac.New(sha256.New, salt)
	m.Write(ikm)
	return m.Sum(nil)
}

func hkdfExpand(prk, info []byte, n int) []byte {
	m := hmac.New(sha256.New, prk)
	m.Write(info)
	m.Write([]byte{1})
	return m.Sum(nil)[:n]
}
