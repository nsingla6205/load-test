package utils

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseVLMWorkerFeatureFlags(t *testing.T) {
	t.Run("ParsesPlainObjectAndJSONStringForms", func(t *testing.T) {
		raw := `{
			"trueZonal":{"vlmWorkerVersion":"9.18.1X1","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"},
			"legacyFeature":"{\"vlmWorkerVersion\":\"9.17.1\",\"VSA_IMAGE_NAME\":\"x-9-17-1p1\",\"VSA_MEDIATOR_IMAGE_NAME\":\"cvo-mediator-x-9-17-1p1\"}"
		}`

		got := ParseVLMWorkerFeatureFlags(raw)

		assert.Equal(t, 2, len(got))
		assert.Equal(t, "9.18.1X1", got["trueZonal"].VlmWorkerVersion)
		assert.Equal(t, "x-9-18-1p2", got["trueZonal"].VSAImageName)
		assert.Equal(t, "cvo-mediator-x-9-18-1p2", got["trueZonal"].VSAMediatorImageName)

		assert.Equal(t, "9.17.1", got["legacyFeature"].VlmWorkerVersion)
		assert.Equal(t, "x-9-17-1p1", got["legacyFeature"].VSAImageName)
		assert.Equal(t, "cvo-mediator-x-9-17-1p1", got["legacyFeature"].VSAMediatorImageName)
	})

	t.Run("SkipsMalformedEntriesButLoadsValidOnes", func(t *testing.T) {
		raw := `{
			"good":{"vlmWorkerVersion":"9.18.1X1","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"},
			"badType":123,
			"badString":"{not-json}"
		}`

		got := ParseVLMWorkerFeatureFlags(raw)

		assert.Equal(t, 1, len(got))
		assert.Equal(t, "9.18.1X1", got["good"].VlmWorkerVersion)
		_, badTypeExists := got["badType"]
		_, badStringExists := got["badString"]
		assert.False(t, badTypeExists)
		assert.False(t, badStringExists)
	})

	t.Run("SkipsEntriesWithEmptyOrMissingVLMWorkerVersion", func(t *testing.T) {
		raw := `{
			"missingVersion":{"VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"},
			"emptyVersion":{"vlmWorkerVersion":"","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"},
			"valid":{"vlmWorkerVersion":"9.18.1X1","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"}
		}`

		got := ParseVLMWorkerFeatureFlags(raw)

		assert.Equal(t, 1, len(got))
		assert.Equal(t, "9.18.1X1", got["valid"].VlmWorkerVersion)
		_, missingExists := got["missingVersion"]
		_, emptyExists := got["emptyVersion"]
		assert.False(t, missingExists)
		assert.False(t, emptyExists)
	})
}

func TestParseVLMWorkerFeatureConfig(t *testing.T) {
	t.Run("AcceptsPlainObject", func(t *testing.T) {
		raw := json.RawMessage(`{"vlmWorkerVersion":"9.18.1X1","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"}`)

		got, ok := parseVLMWorkerFeatureConfig(raw)

		assert.True(t, ok)
		assert.Equal(t, "9.18.1X1", got.VlmWorkerVersion)
		assert.Equal(t, "x-9-18-1p2", got.VSAImageName)
		assert.Equal(t, "cvo-mediator-x-9-18-1p2", got.VSAMediatorImageName)
	})

	t.Run("AcceptsJSONStringEncodedObject", func(t *testing.T) {
		raw := json.RawMessage(`"{\"vlmWorkerVersion\":\"9.18.1X1\",\"VSA_IMAGE_NAME\":\"x-9-18-1p2\",\"VSA_MEDIATOR_IMAGE_NAME\":\"cvo-mediator-x-9-18-1p2\"}"`)

		got, ok := parseVLMWorkerFeatureConfig(raw)

		assert.True(t, ok)
		assert.Equal(t, "9.18.1X1", got.VlmWorkerVersion)
		assert.Equal(t, "x-9-18-1p2", got.VSAImageName)
		assert.Equal(t, "cvo-mediator-x-9-18-1p2", got.VSAMediatorImageName)
	})

	t.Run("RejectsMissingOrEmptyVLMWorkerVersion", func(t *testing.T) {
		missingVersion := json.RawMessage(`{"VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"}`)
		emptyVersion := json.RawMessage(`{"vlmWorkerVersion":"","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"}`)
		whitespaceVersion := json.RawMessage(`{"vlmWorkerVersion":"   ","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"}`)
		encodedWhitespaceVersion := json.RawMessage(`"{\"vlmWorkerVersion\":\"   \",\"VSA_IMAGE_NAME\":\"x-9-18-1p2\",\"VSA_MEDIATOR_IMAGE_NAME\":\"cvo-mediator-x-9-18-1p2\"}"`)

		gotMissing, okMissing := parseVLMWorkerFeatureConfig(missingVersion)
		gotEmpty, okEmpty := parseVLMWorkerFeatureConfig(emptyVersion)
		gotWhitespace, okWhitespace := parseVLMWorkerFeatureConfig(whitespaceVersion)
		gotEncodedWhitespace, okEncodedWhitespace := parseVLMWorkerFeatureConfig(encodedWhitespaceVersion)

		assert.False(t, okMissing)
		assert.Equal(t, VLMWorkerFeatureConfig{}, gotMissing)
		assert.False(t, okEmpty)
		assert.Equal(t, VLMWorkerFeatureConfig{}, gotEmpty)
		assert.False(t, okWhitespace)
		assert.Equal(t, VLMWorkerFeatureConfig{}, gotWhitespace)
		assert.False(t, okEncodedWhitespace)
		assert.Equal(t, VLMWorkerFeatureConfig{}, gotEncodedWhitespace)
	})

	t.Run("TrimsVLMWorkerVersionWhitespace", func(t *testing.T) {
		raw := json.RawMessage(`{"vlmWorkerVersion":" 9.19.1X22 ","VSA_IMAGE_NAME":"x-9-19-1p3","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-19-1p3"}`)
		encodedRaw := json.RawMessage(`"{\"vlmWorkerVersion\":\" 9.19.1X22 \",\"VSA_IMAGE_NAME\":\"x-9-19-1p3\",\"VSA_MEDIATOR_IMAGE_NAME\":\"cvo-mediator-x-9-19-1p3\"}"`)

		got, ok := parseVLMWorkerFeatureConfig(raw)
		gotEncoded, okEncoded := parseVLMWorkerFeatureConfig(encodedRaw)

		assert.True(t, ok)
		assert.Equal(t, "9.19.1X22", got.VlmWorkerVersion)
		assert.True(t, okEncoded)
		assert.Equal(t, "9.19.1X22", gotEncoded.VlmWorkerVersion)
	})
}

func TestGetVLMWorkerFeatureConfig(t *testing.T) {
	const flags = `{"trueZonal":{"vlmWorkerVersion":"9.18.1X1","VSA_IMAGE_NAME":"x-9-18-1p2","VSA_MEDIATOR_IMAGE_NAME":"cvo-mediator-x-9-18-1p2"}}`

	t.Cleanup(func() {
		SetVLMWorkerFeatureFlagsForTesting("")
	})

	t.Run("ReturnsConfigForConfiguredFeature", func(t *testing.T) {
		SetVLMWorkerFeatureFlagsForTesting(flags)

		cfg, ok := GetVLMWorkerFeatureConfig("trueZonal")

		assert.True(t, ok)
		assert.Equal(t, "9.18.1X1", cfg.VlmWorkerVersion)
	})

	t.Run("ReturnsFalseForMissingOrEmptyFeature", func(t *testing.T) {
		SetVLMWorkerFeatureFlagsForTesting(flags)

		_, ok := GetVLMWorkerFeatureConfig("missing")
		assert.False(t, ok)

		_, okEmpty := GetVLMWorkerFeatureConfig("")
		assert.False(t, okEmpty)
	})
}
