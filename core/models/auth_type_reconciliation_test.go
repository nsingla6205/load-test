package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthTypeReconciliationJobPayloadAttributes(t *testing.T) {
	payload := AuthTypeReconciliationJobPayload{
		SelectionType: "poolList",
		FromAuthType:  1,
		ToAuthType:    2,
		DesiredCAURI:  "project/pool/ca",
		BatchSize:     30,
		Results: []AuthTypeReconciliationPoolResult{{
			PoolUUID: "pool-1",
			Status:   "QUEUED",
		}},
	}

	attributes := payload.Attributes()

	require.Equal(t, "poolList", attributes["selectionType"])
	require.Equal(t, 1, attributes["fromAuthType"])
	require.Equal(t, 2, attributes["toAuthType"])
	require.Equal(t, "project/pool/ca", attributes["desiredCaUri"])
	require.Equal(t, 30, attributes["batchSize"])
	results, ok := attributes["results"].([]interface{})
	require.True(t, ok)
	require.Len(t, results, 1)
	require.Equal(t, "pool-1", results[0].(map[string]interface{})["poolUuid"])
}
