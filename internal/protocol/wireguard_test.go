package protocol

import (
	"encoding/base64"
	"testing"
)

func TestGenerateWireGuardKeys(t *testing.T) {
	keys, err := GenerateWireGuardKeys()
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"private": keys.Private, "public": keys.Public} {
		decoded, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil || len(decoded) != 32 {
			t.Fatalf("%s key is invalid: %q, %v", name, value, err)
		}
	}
	derived, err := WireGuardPublicKey(keys.Private)
	if err != nil {
		t.Fatal(err)
	}
	if derived != keys.Public {
		t.Fatalf("derived public key = %q, want %q", derived, keys.Public)
	}
}

func TestWireGuardPublicKeyRejectsInvalidPrivateKey(t *testing.T) {
	for _, value := range []string{"", "not-base64", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := WireGuardPublicKey(value); err == nil {
			t.Fatalf("invalid private key %q was accepted", value)
		}
	}
}
