package utils

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

func TestDecryptBrowserSecretRequiresSessionBoundValidCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	t.Setenv(browserDataEncryptionKeyEnv, base64.StdEncoding.EncodeToString(key))
	id := uuid.New()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	sealed := aead.Seal(nonce, nonce, []byte("https://kernel.example/view?token=x"), []byte(id.String()))
	got, err := DecryptBrowserSecret(sealed, id)
	if err != nil {
		t.Fatalf("DecryptBrowserSecret: %v", err)
	}
	if got != "https://kernel.example/view?token=x" {
		t.Fatalf("decrypted URL = %q", got)
	}
	if _, err := DecryptBrowserSecret(sealed, uuid.New()); err == nil {
		t.Fatal("ciphertext decrypted under a different session")
	}
}

func TestEncryptUserActionResultIsBoundToActionID(t *testing.T) {
	t.Setenv(browserDataEncryptionKeyEnv, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)))
	actionID := uuid.New()
	ciphertext, err := EncryptUserActionResult(`[{"field_name":"OTP","value":"123456"}]`, actionID)
	if err != nil {
		t.Fatalf("EncryptUserActionResult: %v", err)
	}
	plaintext, err := DecryptBrowserSecret(ciphertext, actionID)
	if err != nil {
		t.Fatalf("DecryptBrowserSecret: %v", err)
	}
	if plaintext != `[{"field_name":"OTP","value":"123456"}]` {
		t.Fatalf("decrypted user action result = %q", plaintext)
	}
	if _, err := DecryptBrowserSecret(ciphertext, uuid.New()); err == nil {
		t.Fatal("user action result decrypted with a different action ID")
	}
}
