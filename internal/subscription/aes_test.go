package subscription

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // 测试兼容格式。
	"encoding/base64"
	"testing"
)

func TestDecryptAES(t *testing.T) {
	t.Parallel()
	password := "example-password..."
	want := []byte("vless://example\nss://example")
	encoded := encryptFixture(t, want, password)

	got, err := DecryptAES(encoded, password)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("DecryptAES() = %q, want %q", got, want)
	}
}

func TestDecryptAESRejectsWrongPasswordAndMalformedInput(t *testing.T) {
	t.Parallel()
	encoded := encryptFixture(t, []byte("vless://example"), "correct-password")
	if _, err := DecryptAES(encoded, "wrong-password"); err == nil {
		t.Fatal("wrong password should fail")
	}
	if _, err := DecryptAES([]byte("not base64"), "password"); err == nil {
		t.Fatal("malformed Base64 should fail")
	}
}

func encryptFixture(t *testing.T, plaintext []byte, password string) []byte {
	t.Helper()
	key := md5.Sum([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append([]byte(nil), plaintext...)
	for range padding {
		padded = append(padded, byte(padding))
	}
	iv := []byte("0123456789abcdef")
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	packed := append(append([]byte(nil), iv...), ciphertext...)
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(packed)))
	base64.StdEncoding.Encode(encoded, packed)
	return encoded
}
