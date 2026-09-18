package tlsconfig

import (
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewFIPSClientConfig(t *testing.T) {
	config := NewFIPSClientConfig()

	assert.Equal(t, uint16(tls.VersionTLS12), config.MinVersion)
	assert.Equal(t, FIPSTLS12CipherSuites(), config.CipherSuites)
	assert.Equal(t, []tls.CurveID{
		tls.CurveP256,
		tls.CurveP384,
		tls.CurveP521,
	}, config.CurvePreferences)
	assert.False(t, config.InsecureSkipVerify)
}

func TestNewFIPSClientConfigReturnsIndependentSlices(t *testing.T) {
	first := NewFIPSClientConfig()
	second := NewFIPSClientConfig()

	first.CipherSuites[0] = tls.TLS_RSA_WITH_AES_128_CBC_SHA

	assert.Equal(t, FIPSTLS12CipherSuites(), second.CipherSuites)
}
