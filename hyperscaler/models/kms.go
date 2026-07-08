package hyperscaler

// KmsAccess describes how to authenticate against a customer's KMS crypto key.
// The cloud implementation builds the appropriate client from these fields:
// direct credentials when IsVCPCreated is true, otherwise impersonation of
// ImpersonationEmail using the credentials decoded from EncryptedKeyData.
type KmsAccess struct {
	// IsVCPCreated selects direct credential access (true) versus SDE
	// service-account impersonation (false).
	IsVCPCreated bool
	// ImpersonationEmail is the SDE service account email to impersonate when
	// IsVCPCreated is false.
	ImpersonationEmail string
	// EncryptedKeyData is the encrypted service-account key material used to
	// build credentials.
	EncryptedKeyData string
}

// CryptoKey is a cloud-agnostic view of a KMS crypto key, carrying only the
// fields needed to validate that the key is usable for encryption. The provider
// passes the underlying values through unchanged.
type CryptoKey struct {
	// HasPrimary reports whether the key has a primary key version.
	HasPrimary bool
	// PrimaryAlgorithm is the algorithm of the primary key version.
	PrimaryAlgorithm string
	// PrimaryState is the state of the primary key version (e.g. "ENABLED").
	PrimaryState string
}
