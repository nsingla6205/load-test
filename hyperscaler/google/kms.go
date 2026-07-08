package google

import (
	"context"
	"encoding/base64"
	stdErrors "errors"
	"strings"

	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	googleOauth2 "golang.org/x/oauth2/google"
	"google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/googleapi"
)

// GetKmsCryptoKey fetches the crypto key at keyPath and returns a cloud-agnostic
// view of it. Terminal failures (key unreachable, permission denied) are returned
// as VCP errors; any other failure is returned unwrapped so the caller can treat
// it as transient and retry.
func (gcpService *GcpServices) GetKmsCryptoKey(ctx context.Context, access *hyperscalermodels.KmsAccess, keyPath string) (*hyperscalermodels.CryptoKey, error) {
	kmsService, err := buildKmsService(ctx, access)
	if err != nil {
		return nil, err
	}

	cryptoKey, err := kmsService.Projects.Locations.KeyRings.CryptoKeys.Get(keyPath).Context(ctx).Do()
	if err != nil {
		return nil, classifyKmsError(err)
	}

	result := &hyperscalermodels.CryptoKey{}
	if cryptoKey.Primary != nil {
		result.HasPrimary = true
		result.PrimaryAlgorithm = cryptoKey.Primary.Algorithm
		result.PrimaryState = cryptoKey.Primary.State
	}
	return result, nil
}

// EncryptWithKmsKey encrypts plaintext with the crypto key at keyPath to verify the
// key is usable. The ciphertext is discarded. Terminal failures (key unreachable,
// permission denied) are returned as VCP errors; any other failure is returned
// unwrapped so the caller can treat it as transient and retry.
func (gcpService *GcpServices) EncryptWithKmsKey(ctx context.Context, access *hyperscalermodels.KmsAccess, keyPath, plaintext string) error {
	kmsService, err := buildKmsService(ctx, access)
	if err != nil {
		return err
	}

	req := returnEncryptRequest(plaintext)
	_, err = kmsService.Projects.Locations.KeyRings.CryptoKeys.Encrypt(keyPath, req).Do()
	if err != nil {
		return classifyKmsError(err)
	}
	return nil
}

// buildKmsService builds a Cloud KMS service for the given access: direct credentials
// for VCP-created configs, otherwise SDE service-account impersonation. It is a package
// variable so tests can substitute an httptest-backed service.
var buildKmsService = func(ctx context.Context, access *hyperscalermodels.KmsAccess) (*cloudkms.Service, error) {
	scopeCreds, err := processKmsCredentials(ctx, access.EncryptedKeyData)
	if err != nil {
		return nil, err
	}
	if access.IsVCPCreated {
		return GetDirectKmsService(ctx, scopeCreds)
	}
	return GetImpersonatedKmsService(ctx, access.ImpersonationEmail, scopeCreds)
}

// processKmsCredentials decrypts and decodes the service-account key material and
// builds Google credentials scoped for Cloud KMS.
func processKmsCredentials(ctx context.Context, encryptedKeyData string) (*googleOauth2.Credentials, error) {
	credentialsDecoded, err := utils.DecryptAndDecodeCredentials(encryptedKeyData)
	if err != nil {
		return nil, err
	}
	return googleOauth2.CredentialsFromJSON(ctx, credentialsDecoded, cloudkms.CloudPlatformScope)
}

// returnEncryptRequest builds a Cloud KMS encrypt request for the given plaintext.
func returnEncryptRequest(plainText string) *cloudkms.EncryptRequest {
	encodedText := base64.StdEncoding.EncodeToString([]byte(plainText))
	return &cloudkms.EncryptRequest{
		Plaintext: encodedText,
	}
}

// classifyKmsError maps a Cloud KMS error to a terminal VCP error when it is a
// known non-retryable condition (key unreachable or permission denied). Any other
// error is returned unchanged so the caller can retry it.
func classifyKmsError(err error) error {
	if msg, ok := isKmsKeyUnreachable(err); ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrKMSKeyUnreachable, stdErrors.New(msg))
	}
	if msg, ok := isKmsPermissionDenied(err); ok {
		return vsaerrors.NewVCPError(vsaerrors.ErrKMSPermissionDenied, stdErrors.New(msg))
	}
	return err
}

// isKmsKeyUnreachable inspects Google API errors (and plain errors) to detect Cloud EKM
// reachability issues and returns the user-facing message when matched.
func isKmsKeyUnreachable(err error) (string, bool) {
	var gerr *googleapi.Error
	if stdErrors.As(err, &gerr) {
		body := gerr.Body
		if body != "" {
			lowerBody := strings.ToLower(body)
			if strings.Contains(lowerBody, "key_unreachable") || strings.Contains(lowerBody, "cloud ekm") || strings.Contains(lowerBody, "unreachable") {
				if gerr.Message != "" {
					return gerr.Message, true
				}
				return body, true
			}
		}
		if gerr.Message != "" {
			lowerMsg := strings.ToLower(gerr.Message)
			if strings.Contains(lowerMsg, "key_unreachable") || strings.Contains(lowerMsg, "cloud ekm") || strings.Contains(lowerMsg, "unreachable") {
				return gerr.Message, true
			}
		}
	}
	if err != nil {
		msg := err.Error()
		lowerMsg := strings.ToLower(msg)
		if strings.Contains(lowerMsg, "key_unreachable") || strings.Contains(lowerMsg, "cloud ekm") || strings.Contains(lowerMsg, "unreachable") {
			return msg, true
		}
	}
	return "", false
}

// isKmsPermissionDenied inspects Google API errors to detect permission denied
// (403) errors and returns the user-facing message when matched.
func isKmsPermissionDenied(err error) (string, bool) {
	var gerr *googleapi.Error
	if stdErrors.As(err, &gerr) {
		if gerr.Code == 403 {
			if gerr.Message != "" {
				return gerr.Message, true
			}
			return "Permission denied accessing KMS key", true
		}
	}
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "permission_denied") || strings.Contains(msg, "permission denied") {
			return err.Error(), true
		}
	}
	return "", false
}
