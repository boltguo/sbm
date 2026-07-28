package protocol

import (
	"crypto/rand"
	"encoding/base64"
	"errors"

	"golang.org/x/crypto/curve25519"
)

type WireGuardKeys struct {
	Private string `json:"privateKey"`
	Public  string `json:"publicKey"`
}

func GenerateWireGuardKeys() (WireGuardKeys, error) {
	privateKey := make([]byte, curve25519.ScalarSize)
	if _, err := rand.Read(privateKey); err != nil {
		return WireGuardKeys{}, err
	}
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64
	publicKey, err := curve25519.X25519(privateKey, curve25519.Basepoint)
	if err != nil {
		return WireGuardKeys{}, errors.New("生成 WireGuard 公钥失败")
	}
	return WireGuardKeys{
		Private: base64.StdEncoding.EncodeToString(privateKey),
		Public:  base64.StdEncoding.EncodeToString(publicKey),
	}, nil
}

func WireGuardPublicKey(privateKey string) (string, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(privateKey)
	if err != nil || len(decoded) != curve25519.ScalarSize {
		return "", errors.New("WireGuard 私钥格式无效")
	}
	publicKey, err := curve25519.X25519(decoded, curve25519.Basepoint)
	if err != nil {
		return "", errors.New("WireGuard 私钥格式无效")
	}
	return base64.StdEncoding.EncodeToString(publicKey), nil
}
