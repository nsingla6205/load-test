package utils

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestEncryptUsingQstackCryptoReturnsEncryptedStringOnValidInput(t *testing.T) {
	orig := log.Secret("mySecretPassword")
	encrypted, err := encryptUsingQstackCrypto(orig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if encrypted == nil || *encrypted == "" {
		t.Errorf("expected non-empty encrypted string, got %v", encrypted)
	}
}

func TestEncryptUsingQstackCryptoReturnsDifferentValueThanInput(t *testing.T) {
	orig := log.Secret("anotherSecret")
	encrypted, err := encryptUsingQstackCrypto(orig)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if encrypted != nil && *encrypted == string(orig) {
		t.Errorf("expected encrypted value to differ from input")
	}
}

func TestDecryptUsingQstackCryptoReturnsDecryptedStringOnValidInput(t *testing.T) {
	orig := log.Secret("mySecretPassword")
	encrypted, err := encryptUsingQstackCrypto(orig)
	if err != nil {
		t.Fatalf("unexpected error during encryption: %v", err)
	}
	decrypted, err := decryptUsingQstackCrypto(log.Secret(*encrypted))
	if err != nil {
		t.Fatalf("unexpected error during decryption: %v", err)
	}
	if decrypted == nil || *decrypted != string(orig) {
		t.Errorf("expected decrypted value to match original, got %v", decrypted)
	}
}

func TestDecryptUsingQstackCryptoReturnsErrorOnInvalidInput(t *testing.T) {
	invalid := log.Secret("not-an-encrypted-value")
	decrypted, err := decryptUsingQstackCrypto(invalid)
	if err == nil {
		t.Errorf("expected error for invalid encrypted input, got nil")
	}
	if decrypted != nil {
		t.Errorf("expected nil decrypted value, got %v", decrypted)
	}
}

func TestDecryptAndDecodeCredentialsReturnsDecodedBytesOnValidInput(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("mySecretData"))
	encrypted, err := encryptUsingQstackCrypto(log.Secret(encoded))
	if err != nil {
		t.Fatalf("unexpected error during encryption: %v", err)
	}
	decoded, err := decryptAndDecodeCredentials(*encrypted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(decoded) != "mySecretData" {
		t.Errorf("expected decoded value to match original, got %s", decoded)
	}
}

func TestDecryptAndDecodeCredentialsReturnsErrorOnDecryptFailure(t *testing.T) {
	_, err := decryptAndDecodeCredentials("not-an-encrypted-value")
	if err == nil {
		t.Errorf("expected error for invalid encrypted input, got nil")
	}
}

func TestDecryptAndDecodeCredentialsReturnsErrorOnBase64DecodeFailure(t *testing.T) {
	encrypted, err := encryptUsingQstackCrypto(log.Secret("not-base64"))
	if err != nil {
		t.Fatalf("unexpected error during encryption: %v", err)
	}
	_, err = decryptAndDecodeCredentials(*encrypted)
	if err == nil {
		t.Errorf("expected error for invalid base64, got nil")
	}
}

func TestDecryptServiceCredentialPassword(t *testing.T) {
	t.Run("decrypts V1 ciphertext", func(t *testing.T) {
		encrypted, err := EncryptPassword(log.Secret("plaintext-password"))
		require.NoError(t, err)

		plain, err := DecryptServiceCredentialPassword(*encrypted)
		require.NoError(t, err)
		assert.Equal(t, "plaintext-password", plain)
	})

	t.Run("returns plaintext unchanged", func(t *testing.T) {
		plain, err := DecryptServiceCredentialPassword("already-plain")
		require.NoError(t, err)
		assert.Equal(t, "already-plain", plain)
	})

	t.Run("empty password is no-op", func(t *testing.T) {
		plain, err := DecryptServiceCredentialPassword("")
		require.NoError(t, err)
		assert.Empty(t, plain)
	})
}
