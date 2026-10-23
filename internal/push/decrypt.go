package push

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"encoding/binary"
	"errors"
)

// Decrypt is the browser's half of RFC 8291: it opens a body made by encrypt with the
// subscription's private key and auth secret. Production never calls it; tests use it
// to read exactly what a push service would hand a browser.
func Decrypt(body []byte, uaPrivate *ecdh.PrivateKey, authSecret []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("push: short body")
	}
	salt, idlen := body[:16], int(body[20])
	if binary.BigEndian.Uint32(body[16:20]) != recordSize || len(body) < 21+idlen {
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
