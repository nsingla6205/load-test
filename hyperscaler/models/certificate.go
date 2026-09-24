package hyperscaler

import (
	"time"
)

// CustomCertificate is a struct that represents a certificate independent of Hyperscaler
type CustomCertificate struct {
	Name                       string
	PemCertificate             string
	CreateTime                 *time.Time
	LifeTime                   string
	UpdateTime                 *time.Time
	SubjectCommonName          string
	SubjectAltName             []string
	SerialNumber               string
	PemCertificateChain        []string
	PemCsr                     string
	IssuerCertificateAuthority string
	SubjectOrganization        string
	Region                     string
	CertOwningEntity           string
	CaName                     string
	CaGroupName                string
	CertificateID              string
	// VersionNumber is the certificate version on the issuing service.
	// Populated for OCI (where certificates are explicitly versioned and the
	// version is needed for revoke/rotate flows). On GCP it is left as 0 —
	// CAS does not expose a per-cert version number through the same shape.
	VersionNumber int64
}

// CertificateAuthority describes issuer metadata and its ordered PEM trust chain.
type CertificateAuthority struct {
	Name                string
	State               string
	ExpireTime          time.Time
	PemCertificateChain []string
}

// CustomCertificateParam is a struct that represents the parameters needed to create a certificate
type CustomCertificateParam struct {
	Region           string
	CertOwningEntity string
	CaName           string
	CaPoolName       string
	CertificateID    string
	Domains          []string
	CommonName       string
}

// CustomSecret is a struct that represents a secret
type CustomSecret struct {
	Name               string
	SecretOwningEntity string
	Region             string
	CreateTime         *time.Time
	LifeTime           *time.Time
	SecretVersion      *CustomSecretVersion
}

// CustomSecretVersion is a struct that represents a version of a secret
type CustomSecretVersion struct {
	Name               string
	Value              string
	SecretOwningEntity string
	Region             string
}

type CustomCertificateResponse struct {
	Certificate *CustomCertificate
	Secret      *CustomSecret
}

type CustomCloudDNSRecord struct {
	RecordName  string
	Type        string
	TTL         int64
	Data        string // IP address
	ManagedZone string
}
