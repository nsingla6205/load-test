// Package tlsconfig provides the transport-security policy used for
// control-plane connections to the ONTAP data plane.
package tlsconfig

import "crypto/tls"

// fipsTLS12CipherSuites contains the TLS 1.2 suites approved for CP-to-DP
// traffic. TLS 1.3 suites are selected by Go and cannot be configured here;
// Go only enables AES-GCM and ChaCha20-Poly1305 suites for TLS 1.3.
//
// ChaCha20-Poly1305 is deliberately excluded from this TLS 1.2 list because
// it is not an approved algorithm in the FIPS cryptographic module boundary.
var fipsTLS12CipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
}

// FIPSTLS12CipherSuites returns a copy of the approved TLS 1.2 cipher-suite
// policy.
func FIPSTLS12CipherSuites() []uint16 {
	return append([]uint16(nil), fipsTLS12CipherSuites...)
}

// NewFIPSClientConfig returns the common CP-to-DP TLS policy. Callers remain
// responsible for configuring trust roots, client certificates, ServerName,
// and (for documented legacy exceptions only) InsecureSkipVerify.
func NewFIPSClientConfig() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS12,
		CipherSuites: FIPSTLS12CipherSuites(),
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.CurveP384,
			tls.CurveP521,
		},
	}
}
