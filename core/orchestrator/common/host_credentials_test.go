package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildHostClientCACertificateNameForChainIndex(t *testing.T) {
	assert.Equal(t, "cluster-client-ca-0", BuildHostClientCACertificateNameForChainIndex("cluster", 0))
	assert.Equal(t, "cluster-client-ca-2", BuildHostClientCACertificateNameForChainIndex("  cluster  ", 2))
}

func TestBuildHostServerCACertificateNameForChainIndex(t *testing.T) {
	assert.Equal(t, "cluster-server-ca-1", BuildHostServerCACertificateNameForChainIndex("cluster", 1))
	assert.Equal(t, "cluster-server-ca-0", BuildHostServerCACertificateNameForChainIndex("  cluster  ", 0))
}
