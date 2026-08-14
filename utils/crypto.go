package utils

import (
	"encoding/base64"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/crypto"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

const passphrase = "Koi aisa kaise kar sakta hai"

var (
	// EncryptPassword accepts a golang string object and returns it encrypted
	EncryptPassword = encryptUsingQstackCrypto

	// DecryptPassword accepts a golang string object and returns it decrypted
	DecryptPassword = decryptUsingQstackCrypto

	// DecryptAndDecodeCredentials decrypts the password and decodes the Base64 encoded credentials.
	DecryptAndDecodeCredentials = decryptAndDecodeCredentials
)

func encryptUsingQstackCrypto(password log.Secret) (*string, error) {
	sh := crypto.NewSecretsHandler(crypto.V1)
	return sh.Encrypt(passphrase, string(password))
}

func decryptUsingQstackCrypto(password log.Secret) (*string, error) {
	sh := crypto.NewSecretsHandler(crypto.V1)
	return sh.Decrypt(passphrase, string(password))
}

// decryptAndDecodeCredentials decrypts the password and decodes the Base64 encoded credentials.
func decryptAndDecodeCredentials(secretPassword string) ([]byte, error) {
	// Decrypt the password
	decryptKey, err := DecryptPassword(log.Secret(secretPassword))
	if err != nil {
		return nil, err
	}

	// Decode the Base64 encoded credentials
	credentialsDecoded, err := base64.StdEncoding.DecodeString(*decryptKey)
	if err != nil {
		return nil, err
	}

	return credentialsDecoded, nil
}

// DecryptServiceCredentialPassword decrypts an encrypted service credential password.
// Plaintext values (non V1 ciphertext) are returned unchanged for backward compatibility.
func DecryptServiceCredentialPassword(encrypted string) (string, error) {
	encrypted = strings.TrimSpace(encrypted)
	if encrypted == "" {
		return "", nil
	}
	if !strings.HasPrefix(encrypted, "V1:") {
		return encrypted, nil
	}
	decrypted, err := DecryptPassword(log.Secret(encrypted))
	if err != nil {
		return "", err
	}
	return *decrypted, nil
}
