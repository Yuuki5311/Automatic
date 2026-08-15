package credentialcrypto

import (
	"os"
	"testing"
)

const testKey = "0123456789abcdef"

func TestEncryptDecryptRoundTrip(t *testing.T) {
	client, err := New([]byte(testKey))
	if err != nil {
		t.Fatal(err)
	}
	plain := "test123456"
	enc, err := client.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if enc == plain {
		t.Fatal("expected ciphertext to differ from plaintext")
	}
	got, err := client.Decrypt(enc)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("decrypt mismatch: got %q want %q", got, plain)
	}
}

func TestTryDecryptPlaintextPassthrough(t *testing.T) {
	client, err := New([]byte(testKey))
	if err != nil {
		t.Fatal(err)
	}
	plain := "plain_account_001"
	if got := client.TryDecrypt(plain); got != plain {
		t.Fatalf("expected passthrough, got %q", got)
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv(DefaultKeyEnvName, testKey)
	client, err := NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := client.Encrypt("hello")
	if err != nil {
		t.Fatal(err)
	}
	if client.TryDecrypt(enc) != "hello" {
		t.Fatal("env-based client decrypt failed")
	}
}

func TestNewFromEnvMissing(t *testing.T) {
	os.Unsetenv(DefaultKeyEnvName)
	if _, err := NewFromEnv(); err == nil {
		t.Fatal("expected error when env var is missing")
	}
}
