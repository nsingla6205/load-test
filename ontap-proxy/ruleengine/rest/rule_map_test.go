package rules_v2

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/ruleengine/dsl"
)

func TestGetProxyRules(t *testing.T) {
	t.Run("ShouldReturnNonEmptyRuleMap", func(t *testing.T) {
		rules := GetProxyRules()
		assert.NotEmpty(t, rules)
	})

	t.Run("ShouldContainPrivateCLIVolumeRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/private/cli/volume"]
		assert.True(t, ok, "Should have rule for /api/private/cli/volume")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainPrivateCLIVolumeRenameRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/private/cli/volume/rename"]
		assert.True(t, ok, "Should have rule for /api/private/cli/volume/rename")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainPrivateCLIVolumeShowFootprintRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/private/cli/volume/show-footprint"]
		assert.True(t, ok, "Should have rule for /api/private/cli/volume/show-footprint")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainPrivateCLIVolumeCloneRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/private/cli/volume/clone"]
		assert.True(t, ok, "Should have rule for /api/private/cli/volume/clone")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainPrivateCLIVolumeCloneSplitStartRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/private/cli/volume/clone/split/start"]
		assert.True(t, ok, "Should have rule for /api/private/cli/volume/clone/split/start")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainStorageVolumesRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/storage/volumes"]
		assert.True(t, ok, "Should have rule for /api/storage/volumes")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
	})

	t.Run("ShouldContainStorageVolumesUUIDRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/storage/volumes/{uuid}"]
		assert.True(t, ok, "Should have rule for /api/storage/volumes/{uuid}")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainStorageLUNsRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/storage/luns"]
		assert.True(t, ok, "Should have rule for /api/storage/luns")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainStorageLUNsUUIDRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/storage/luns/{uuid}"]
		assert.True(t, ok, "Should have rule for /api/storage/luns/{uuid}")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainStorageFlexCacheRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/storage/flexcache/flexcaches"]
		assert.True(t, ok, "Should have rule for /api/storage/flexcache/flexcaches")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainStorageFlexCacheUUIDRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/storage/flexcache/flexcaches/{uuid}"]
		assert.True(t, ok, "Should have rule for /api/storage/flexcache/flexcaches/{uuid}")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainStorageAggregatesRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/storage/aggregates"]
		assert.True(t, ok, "Should have rule for /api/storage/aggregates")
		assert.NotNil(t, rule.GET)
	})

	t.Run("ShouldContainClusterCounterTablesRule", func(t *testing.T) {
		rules := GetProxyRules()
		rule, ok := rules["/api/cluster/counter/tables/*"]
		assert.True(t, ok, "Should have rule for /api/cluster/counter/tables/*")
		assert.NotNil(t, rule.GET)
		assert.NotNil(t, rule.POST)
		assert.NotNil(t, rule.PATCH)
		assert.NotNil(t, rule.DELETE)
	})

	t.Run("ShouldContainQoSPoliciesRules", func(t *testing.T) {
		rules := GetProxyRules()
		collection, ok := rules["/api/storage/qos/policies"]
		assert.True(t, ok, "Should have rule for /api/storage/qos/policies")
		assert.NotNil(t, collection.GET)
		assert.NotNil(t, collection.POST)
		assert.NotNil(t, collection.PATCH)
		assert.NotNil(t, collection.DELETE)

		instance, ok := rules["/api/storage/qos/policies/{uuid}"]
		assert.True(t, ok, "Should have rule for /api/storage/qos/policies/{uuid}")
		assert.NotNil(t, instance.GET)
		assert.NotNil(t, instance.POST)
		assert.Nil(t, instance.PATCH)
		assert.Nil(t, instance.DELETE)
	})

	t.Run("ShouldContainNestedQoSSettingsRules", func(t *testing.T) {
		rules := GetProxyRules()
		for _, path := range []string{
			"/api/private/cli/qos/settings/nested-qos-svm-vol",
			"/api/private/cli/settings/nested-qos-svm-vol",
		} {
			rule, ok := rules[path]
			assert.True(t, ok, "Should have rule for %s", path)
			assert.NotNil(t, rule.GET)
			assert.NotNil(t, rule.POST)
			assert.NotNil(t, rule.PATCH)
			assert.NotNil(t, rule.DELETE)
		}
	})

	t.Run("ShouldNotContainQtreeDenyRules", func(t *testing.T) {
		rules := GetProxyRules()
		_, okCollection := rules["/api/storage/qtrees"]
		_, okInstance := rules["/api/storage/qtrees/{uuid}/*"]
		assert.False(t, okCollection, "qtrees collection should pass through (no deny rule)")
		assert.False(t, okInstance, "qtrees instance should pass through (no deny rule)")
	})
}

func TestClusterCounterTablesRule(t *testing.T) {
	t.Run("WhenGET_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/cluster/counter/tables/*"]
		req := httptest.NewRequest(http.MethodGet, "/api/cluster/counter/tables", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "GET should be denied for cluster counter tables")
		assert.Contains(t, reason, "Cluster counter tables not allowed")
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/cluster/counter/tables/*"]
		req := httptest.NewRequest(http.MethodPost, "/api/cluster/counter/tables", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for cluster counter tables")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/cluster/counter/tables/*"]
		req := httptest.NewRequest(http.MethodPatch, "/api/cluster/counter/tables/qos_detail", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH should be denied for cluster counter tables")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/cluster/counter/tables/*"]
		req := httptest.NewRequest(http.MethodDelete, "/api/cluster/counter/tables/wafl/rows/123", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "DELETE should be denied for cluster counter tables")
		assert.NotEmpty(t, reason)
	})
}

func TestPrivateCLIVolumeRule(t *testing.T) {
	origValidateCreation := validatePrivateCLIVolumeCreation
	origValidateModification := validatePrivateCLIVolumeModification
	origValidateDeletion := validatePrivateCLIVolumeDeletion
	defer func() {
		validatePrivateCLIVolumeCreation = origValidateCreation
		validatePrivateCLIVolumeModification = origValidateModification
		validatePrivateCLIVolumeDeletion = origValidateDeletion
	}()

	validatePrivateCLIVolumeCreation = func(r *http.Request) (bool, string) { return true, "" }
	validatePrivateCLIVolumeModification = func(r *http.Request) (bool, string) { return true, "" }
	validatePrivateCLIVolumeDeletion = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		req := httptest.NewRequest(http.MethodGet, "/api/private/cli/volume", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for private CLI volume")
	})

	t.Run("WhenPOSTWithRequiredFields_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		body := bytes.NewBufferString(`{"volume":"vol1","vserver":"vs0","size":1073741824}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with required fields should be allowed")
	})

	t.Run("WhenPOSTWithoutVolume_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		body := bytes.NewBufferString(`{"vserver":"vs0","size":1073741824}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST without volume should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTWithValidSpaceGuarantee_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		body := bytes.NewBufferString(`{"volume":"vol1","vserver":"vs0","size":1073741824,"space_guarantee":"none"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with space_guarantee='none' should be allowed")
	})

	t.Run("WhenPOSTWithInvalidSpaceGuarantee_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		body := bytes.NewBufferString(`{"volume":"vol1","vserver":"vs0","size":1073741824,"space_guarantee":"volume"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST with invalid space_guarantee should be denied")
		assert.NotEmpty(t, reason)
	})

	// snaplock_type is no longer allowlist-validated; previously-denied values must be allowed (regression guard).
	t.Run("WhenPOSTWithPreviouslyDeniedSnaplockTypeCompliance_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		body := bytes.NewBufferString(`{"volume":"vol1","vserver":"vs0","size":1073741824,"snaplock_type":"compliance"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with snaplock_type=compliance should be allowed (snaplock type not validated); got reason: %s", reason)
	})

	t.Run("WhenPATCHWithQueryParams_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		body := bytes.NewBufferString(`{"size":2147483648}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume?vserver=vs1&volume=vol1", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with vserver and volume query params should be allowed")
	})

	// snaplock_type is no longer allowlist-validated; previously-denied values must be allowed (regression guard).
	t.Run("WhenPATCHWithPreviouslyDeniedSnaplockTypeCompliance_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		body := bytes.NewBufferString(`{"snaplock_type":"compliance"}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume?vserver=vs1&volume=vol1", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with snaplock_type=compliance should be allowed (snaplock type not validated); got reason: %s", reason)
	})

	t.Run("WhenDELETEWithQueryParams_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume"]
		req := httptest.NewRequest(http.MethodDelete, "/api/private/cli/volume?vserver=vs1&volume=vol1", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "DELETE with vserver and volume query params should be allowed")
	})
}

func TestPrivateCLIVolumeRenameRule(t *testing.T) {
	origValidateRename := validatePrivateCLIVolumeRename
	defer func() { validatePrivateCLIVolumeRename = origValidateRename }()

	validatePrivateCLIVolumeRename = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenPATCHWithNewnameAndValidatorSucceeds_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/rename"]
		body := bytes.NewBufferString(`{"newname":"vol_renamed"}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with newname and passing validator should be allowed")
	})

	t.Run("WhenPATCHWithoutNewname_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/rename"]
		body := bytes.NewBufferString(`{}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH without newname should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenGET_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/rename"]
		req := httptest.NewRequest(http.MethodGet, "/api/private/cli/volume/rename", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "GET should be denied for volume rename")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/rename"]
		body := bytes.NewBufferString(`{"newname":"vol_renamed"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for volume rename")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/rename"]
		req := httptest.NewRequest(http.MethodDelete, "/api/private/cli/volume/rename?vserver=vs1&volume=vol1", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "DELETE should be denied for volume rename")
		assert.NotEmpty(t, reason)
	})
}

func TestPrivateCLIVolumeShowFootprintRule(t *testing.T) {
	t.Run("WhenGET_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/show-footprint"]
		req := httptest.NewRequest(http.MethodGet, "/api/private/cli/volume/show-footprint?vserver=vs1&volume=vol1", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "GET should be denied for private CLI volume show-footprint")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/show-footprint"]
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/show-footprint", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for volume show-footprint")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/show-footprint"]
		req := httptest.NewRequest(http.MethodPatch, "/api/private/cli/volume/show-footprint", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH should be denied for volume show-footprint")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/show-footprint"]
		req := httptest.NewRequest(http.MethodDelete, "/api/private/cli/volume/show-footprint", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "DELETE should be denied for volume show-footprint")
		assert.NotEmpty(t, reason)
	})
}

func TestPrivateCLIVolumeCloneRule(t *testing.T) {
	origValidateCloneCreate := validatePrivateCLIVolumeCloneCreate
	defer func() { validatePrivateCLIVolumeCloneCreate = origValidateCloneCreate }()
	validatePrivateCLIVolumeCloneCreate = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenPOSTWithRequiredFields_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/clone"]
		body := bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1","parent_volume":"src1"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with required clone fields should be allowed, reason: %s", reason)
	})

	t.Run("WhenPOSTWithoutParentVolumeOrB_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/clone"]
		body := bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.Contains(t, reason, "parent_volume or b")
	})

	t.Run("WhenGET_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/clone"]
		req := httptest.NewRequest(http.MethodGet, "/api/private/cli/volume/clone", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.NotEmpty(t, reason)
	})
}

func TestPrivateCLIVolumeCloneSplitStartRule(t *testing.T) {
	origValidateCloneSplit := validatePrivateCLIVolumeCloneSplit
	defer func() { validatePrivateCLIVolumeCloneSplit = origValidateCloneSplit }()
	validatePrivateCLIVolumeCloneSplit = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenPOSTWithRequiredFields_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/clone/split/start"]
		body := bytes.NewBufferString(`{"vserver":"vs0","flexclone":"clone1"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone/split/start", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST split start with required fields should be allowed, reason: %s", reason)
	})

	t.Run("WhenPOSTWithoutFlexclone_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/private/cli/volume/clone/split/start"]
		body := bytes.NewBufferString(`{"vserver":"vs0"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/private/cli/volume/clone/split/start", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.NotEmpty(t, reason)
	})
}

func TestStorageVolumesRule(t *testing.T) {
	// Save and restore original validator to avoid cross-test pollution
	origValidateVolumeCreation := validateVolumeCreation
	defer func() { validateVolumeCreation = origValidateVolumeCreation }()

	// Force creation validator to pass for these tests
	validateVolumeCreation = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		req := httptest.NewRequest(http.MethodGet, "/api/storage/volumes", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for storage volumes")
	})

	t.Run("WhenPOSTWithRequiredFields_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824, "name": "test-volume"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with required fields should be allowed")
	})

	t.Run("WhenPOSTWithValidGuaranteeType_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824, "name": "test-volume", "guarantee": {"type": "none"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with guarantee.type='none' should be allowed")
	})

	t.Run("WhenPOSTWithInvalidGuaranteeType_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824, "name": "test-volume", "guarantee": {"type": "volume"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST with invalid guarantee.type should be denied")
		assert.NotEmpty(t, reason)
	})

	// snaplock.type is no longer allowlist-validated; previously-denied values must be allowed (regression guard).
	t.Run("WhenPOSTWithPreviouslyDeniedSnaplockTypeCompliance_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824, "name": "test-volume", "snaplock": {"type": "compliance"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with snaplock.type=compliance should be allowed (snaplock type not validated); got reason: %s", reason)
	})

	t.Run("WhenPOSTWithSpaceSizeOnly_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"name": "test-volume", "space": {"size": 1073741824}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with name and space.size (no top-level size) should be allowed")
	})

	t.Run("WhenPOSTWithBothSizeAndSpaceSize_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824, "name": "test-volume", "space": {"size": 2147483648}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with both 'size' and 'space.size' should be allowed (core uses space.size); reason: %s", reason)
	})

	t.Run("WhenPOSTWithoutSizeOrSpaceSize_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"name": "test-volume"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST without 'size' or 'space.size' should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTWithAutosizeModeGrow_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824, "name": "test-volume", "autosize": {"mode": "grow"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST with autosize.mode=grow should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTWithAutosizeModeOff_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824, "name": "test-volume", "autosize": {"mode": "off"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with autosize.mode=off should be allowed")
	})

	t.Run("WhenPOSTCloneWithIsFlexcloneWithoutSize_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"name":"clone-volume","clone":{"is_flexclone":true,"parent_volume":{"name":"src-volume"}}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST clone with is_flexclone should be allowed without size; reason: %s", reason)
	})

	t.Run("WhenPOSTWithoutNameField_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		body := bytes.NewBufferString(`{"size": 1073741824}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST without 'name' field should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes"]
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH should be denied for storage volumes collection")
		assert.NotEmpty(t, reason)
	})
}

func TestFPolicyPersistentStoreRule_AutosizeMode(t *testing.T) {
	const path = "/api/protocols/fpolicy/4f643fb4-fd21-11e8-ae49-0050568e2c1e/persistent-stores"

	rules := GetProxyRules()
	rule, ok := rules["/api/protocols/fpolicy/{uuid}/persistent-stores"]
	assert.True(t, ok, "Should have a rule for FPolicy persistent-store collection")

	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{
			name:        "WhenAutosizeModeOmitted_ShouldAllow",
			body:        `{"name":"ps1","volume":"psvol1","size":1073741824}`,
			wantAllowed: true,
		},
		{
			name:        "WhenAutosizeModeOff_ShouldAllow",
			body:        `{"name":"ps1","volume":"psvol1","size":1073741824,"autosize_mode":"off"}`,
			wantAllowed: true,
		},
		{
			name:        "WhenAutosizeModeGrow_ShouldDeny",
			body:        `{"name":"ps1","volume":"psvol1","size":1073741824,"autosize_mode":"grow"}`,
			wantAllowed: false,
		},
		{
			name:        "WhenAutosizeModeGrowShrink_ShouldDeny",
			body:        `{"name":"ps1","volume":"psvol1","size":1073741824,"autosize_mode":"grow_shrink"}`,
			wantAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			action := rule.GetAction(req)
			assert.NotNil(t, action)
			allowed, reason := action.ShouldAllow(req)
			assert.Equal(t, tt.wantAllowed, allowed, "reason: %s", reason)
		})
	}
}

func TestPrivateCLIFPolicyPersistentStoreRule_AutosizeMode(t *testing.T) {
	const path = "/api/private/cli/vserver/fpolicy/persistent-store"

	rules := GetProxyRules()
	rule, ok := rules[path]
	assert.True(t, ok, "Should have a rule for private CLI FPolicy persistent-store")

	tests := []struct {
		name        string
		body        string
		wantAllowed bool
	}{
		{
			name:        "WhenAutosizeModeOmitted_ShouldAllow",
			body:        `{"vserver":"vs1","persistent-store":"ps1","volume":"psvol1","size":"1GB"}`,
			wantAllowed: true,
		},
		{
			name:        "WhenAutosizeModeOff_ShouldAllow",
			body:        `{"vserver":"vs1","persistent-store":"ps1","volume":"psvol1","size":"1GB","autosize-mode":"off"}`,
			wantAllowed: true,
		},
		{
			name:        "WhenAutosizeModeGrow_ShouldDeny",
			body:        `{"vserver":"vs1","persistent-store":"ps1","volume":"psvol1","size":"1GB","autosize-mode":"grow"}`,
			wantAllowed: false,
		},
		{
			name:        "WhenAutosizeModeGrowShrink_ShouldDeny",
			body:        `{"vserver":"vs1","persistent-store":"ps1","volume":"psvol1","size":"1GB","autosize-mode":"grow_shrink"}`,
			wantAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			action := rule.GetAction(req)
			assert.NotNil(t, action)
			allowed, reason := action.ShouldAllow(req)
			assert.Equal(t, tt.wantAllowed, allowed, "reason: %s", reason)
		})
	}
}

func TestStorageVolumesUUIDRule(t *testing.T) {
	// Save and restore originals
	origValidateVolumeModification := validateVolumeModification
	origValidateVolumeDeletion := validateVolumeDeletion
	defer func() {
		validateVolumeModification = origValidateVolumeModification
		validateVolumeDeletion = origValidateVolumeDeletion
	}()

	// Ensure modification validator passes for these tests unless overridden per subtest
	validateVolumeModification = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		req := httptest.NewRequest(http.MethodGet, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for specific volume")
	})

	t.Run("WhenPATCHWithValidBody_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"size": 2147483648}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with valid body should be allowed")
	})

	t.Run("WhenPATCHWithValidGuaranteeTypeNone_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"size": 2147483648, "name": "updated-volume", "guarantee": {"type": "none"}}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with guarantee.type='none' should be allowed")
	})

	t.Run("WhenPATCHWithInvalidGuaranteeTypeVolume_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"size": 2147483648, "name": "updated-volume", "guarantee": {"type": "volume"}}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH with guarantee.type='volume' should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCHWithInvalidGuaranteeType_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"size": 2147483648, "name": "updated-volume", "guarantee": {"type": "invalid"}}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH with invalid guarantee.type should be denied")
		assert.NotEmpty(t, reason)
	})

	// snaplock.type is no longer allowlist-validated; previously-denied values must be allowed (regression guard).
	t.Run("WhenPATCHWithPreviouslyDeniedSnaplockTypeCompliance_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"size": 2147483648, "snaplock": {"type": "compliance"}}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with snaplock.type=compliance should be allowed (snaplock type not validated); got reason: %s", reason)
	})

	t.Run("WhenPATCHWithOnlyComment_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"comment": "just a comment"}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with only comment should be allowed")
	})

	t.Run("WhenPATCHWithAutosizeModeGrow_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"autosize": {"maximum": 2147483648, "mode": "grow"}}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH with autosize.mode=grow should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCHWithAutosizeModeOff_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"autosize": {"mode": "off"}}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH with autosize.mode=off should be allowed")
	})

	t.Run("WhenPATCHWithBothSizeAndSpaceSize_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		body := bytes.NewBufferString(`{"size": 2147483648, "space": {"size": 3221225472}}`)
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH with both 'size' and 'space.size' should be denied")
		assert.Contains(t, reason, "use one or the other")
	})

	t.Run("WhenDELETE_ShouldAllow", func(t *testing.T) {
		// Mock deletion validator BEFORE building rules
		validateVolumeDeletion = func(r *http.Request) (bool, string) { return true, "" }
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "DELETE should be allowed for specific volume")
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/volumes/{uuid}"]
		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for specific volume")
		assert.NotEmpty(t, reason)
	})
}

func TestStorageFlexCacheRule(t *testing.T) {
	origValidateFlexCacheCreation := validateFlexCacheCreation
	defer func() { validateFlexCacheCreation = origValidateFlexCacheCreation }()

	validateFlexCacheCreation = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		req := httptest.NewRequest(http.MethodGet, "/api/storage/flexcache/flexcaches", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for flexcache collection")
	})

	t.Run("WhenPOSTWithRequiredFields_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		body := bytes.NewBufferString(`{"name":"fc1","size":1073741824,"svm":{"name":"svm1"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with required fields should be allowed")
	})

	t.Run("WhenPOSTWithoutSize_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		body := bytes.NewBufferString(`{"name":"fc1","svm":{"name":"svm1"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST without size should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTWithoutSvmName_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		body := bytes.NewBufferString(`{"name":"fc1","size":1073741824,"svm":{}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST without svm.name should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTWithValidGuaranteeType_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		body := bytes.NewBufferString(`{"name":"fc1","size":1073741824,"svm":{"name":"svm1"},"guarantee":{"type":"none"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with guarantee.type='none' should be allowed")
	})

	t.Run("WhenPOSTWithInvalidGuaranteeType_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		body := bytes.NewBufferString(`{"name":"fc1","size":1073741824,"svm":{"name":"svm1"},"guarantee":{"type":"volume"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST with invalid guarantee.type should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTWithRelativeSizeEnabledFalse_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		body := bytes.NewBufferString(`{"name":"fc1","size":1073741824,"svm":{"name":"svm1"},"relative_size":{"enabled":false}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with relative_size.enabled=false should be allowed")
	})

	t.Run("WhenPOSTWithRelativeSizeEnabledTrue_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		body := bytes.NewBufferString(`{"name":"fc1","size":1073741824,"svm":{"name":"svm1"},"relative_size":{"enabled":true}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST with relative_size.enabled=true should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/flexcache/flexcaches", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH should be denied for flexcache collection")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches"]
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/flexcache/flexcaches", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "DELETE should be denied for flexcache collection")
		assert.NotEmpty(t, reason)
	})
}

func TestStorageFlexCacheUUIDRule(t *testing.T) {
	origValidateFlexCacheDeletion := validateFlexCacheDeletion
	defer func() { validateFlexCacheDeletion = origValidateFlexCacheDeletion }()
	validateFlexCacheDeletion = func(r *http.Request) (bool, string) { return true, "" }

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches/{uuid}"]
		req := httptest.NewRequest(http.MethodGet, "/api/storage/flexcache/flexcaches/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for specific flexcache")
	})

	t.Run("WhenDELETE_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches/{uuid}"]
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/flexcache/flexcaches/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "DELETE should be allowed for specific flexcache")
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches/{uuid}"]
		req := httptest.NewRequest(http.MethodPost, "/api/storage/flexcache/flexcaches/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for specific flexcache")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCH_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/flexcache/flexcaches/{uuid}"]
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/flexcache/flexcaches/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH should be allowed for specific flexcache")
	})
}

func TestStorageAggregatesRule(t *testing.T) {
	getTests := []struct {
		name         string
		rulePath     string
		requestPath  string
		serviceLevel string
		wantAllowed  bool
		wantReason   string
	}{
		{
			name:         "WhenCollectionGETForEXTREME_ShouldAllow",
			rulePath:     "/api/storage/aggregates",
			requestPath:  "/api/storage/aggregates",
			serviceLevel: commonparams.ServiceLevelExtreme,
			wantAllowed:  true,
		},
		{
			name:         "WhenCollectionGETForPREMIUM_ShouldAllow",
			rulePath:     "/api/storage/aggregates",
			requestPath:  "/api/storage/aggregates",
			serviceLevel: commonparams.ServiceLevelPremium,
			wantAllowed:  true,
		},
		{
			name:         "WhenCollectionGETForSTANDARD_ShouldAllow",
			rulePath:     "/api/storage/aggregates",
			requestPath:  "/api/storage/aggregates",
			serviceLevel: "STANDARD",
			wantAllowed:  true,
		},
		{
			name:         "WhenCollectionGETForFLEX_ShouldDeny",
			rulePath:     "/api/storage/aggregates",
			requestPath:  "/api/storage/aggregates",
			serviceLevel: models.ServiceLevelFLEX,
			wantReason:   "Aggregate listing not allowed",
		},
		{
			name:         "WhenInstanceGETForEXTREME_ShouldAllow",
			rulePath:     "/api/storage/aggregates/{uuid}",
			requestPath:  "/api/storage/aggregates/550e8400-e29b-41d4-a716-446655440000",
			serviceLevel: commonparams.ServiceLevelExtreme,
			wantAllowed:  true,
		},
		{
			name:         "WhenInstanceGETForPREMIUM_ShouldAllow",
			rulePath:     "/api/storage/aggregates/{uuid}",
			requestPath:  "/api/storage/aggregates/550e8400-e29b-41d4-a716-446655440000",
			serviceLevel: commonparams.ServiceLevelPremium,
			wantAllowed:  true,
		},
		{
			name:         "WhenInstanceGETForSTANDARD_ShouldAllow",
			rulePath:     "/api/storage/aggregates/{uuid}",
			requestPath:  "/api/storage/aggregates/550e8400-e29b-41d4-a716-446655440000",
			serviceLevel: "STANDARD",
			wantAllowed:  true,
		},
		{
			name:         "WhenInstanceGETForFLEX_ShouldDeny",
			rulePath:     "/api/storage/aggregates/{uuid}",
			requestPath:  "/api/storage/aggregates/550e8400-e29b-41d4-a716-446655440000",
			serviceLevel: models.ServiceLevelFLEX,
			wantReason:   "Aggregate details not allowed",
		},
	}

	for _, tt := range getTests {
		t.Run(tt.name, func(t *testing.T) {
			rule := GetProxyRules()[tt.rulePath]
			req := requestWithAuthData(t, http.MethodGet, tt.requestPath, &models.AuthData{
				ServiceLevel: tt.serviceLevel,
			})

			action := rule.GetAction(req)

			assert.NotNil(t, action)
			allowed, reason := action.ShouldAllow(req)
			assert.Equal(t, tt.wantAllowed, allowed)
			assert.Equal(t, tt.wantReason, reason)
		})
	}

	t.Run("WhenCollectionGETWithoutAuthData_ShouldDeny", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/aggregates"]
		req := httptest.NewRequest(http.MethodGet, "/api/storage/aggregates", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.Equal(t, "Aggregate listing not allowed", reason)
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/aggregates"]
		req := httptest.NewRequest(http.MethodPost, "/api/storage/aggregates", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for aggregates")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/aggregates"]
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/aggregates", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH should be denied for aggregates")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/storage/aggregates"]
		req := httptest.NewRequest(http.MethodDelete, "/api/storage/aggregates", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "DELETE should be denied for aggregates")
		assert.NotEmpty(t, reason)
	})
}

func TestSecurityCertificatesRule(t *testing.T) {
	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		req := httptest.NewRequest(http.MethodGet, "/api/security/certificates", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for certificates collection")
	})

	t.Run("WhenPOSTWithTypeServer_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		body := bytes.NewBufferString(`{"type": "server", "common_name": "test.example.com"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/security/certificates", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with type=server should be allowed")
	})

	t.Run("WhenPOSTWithTypeClient_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		body := bytes.NewBufferString(`{"type": "client", "common_name": "test.example.com"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/security/certificates", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with type=client should be allowed")
	})

	t.Run("WhenPOSTWithTypeServerCA_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		body := bytes.NewBufferString(`{"type": "server_ca", "common_name": "test.example.com"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/security/certificates", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with type=server_ca should be allowed")
	})

	t.Run("WhenPOSTWithTypeClientCA_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		body := bytes.NewBufferString(`{"type": "client_ca", "common_name": "test.example.com"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/security/certificates", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with type=client_ca should be allowed")
	})

	t.Run("WhenPOSTWithTypeRootCA_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		body := bytes.NewBufferString(`{"type": "root_ca", "common_name": "test.example.com"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/security/certificates", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST with type=root_ca should be denied")
		assert.Contains(t, reason, "type")
	})

	t.Run("WhenPOSTWithoutType_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		body := bytes.NewBufferString(`{"common_name": "test.example.com"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/security/certificates", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "POST without type field should be allowed (IfPresentThenValue passes)")
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		req := httptest.NewRequest(http.MethodPatch, "/api/security/certificates", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH should be denied for certificates collection")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates"]
		req := httptest.NewRequest(http.MethodDelete, "/api/security/certificates", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "DELETE should be denied for certificates collection")
		assert.NotEmpty(t, reason)
	})
}

func TestSecurityCertificatesUUIDRule(t *testing.T) {
	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates/{uuid}"]
		req := httptest.NewRequest(http.MethodGet, "/api/security/certificates/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for specific certificate")
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates/{uuid}"]
		req := httptest.NewRequest(http.MethodPost, "/api/security/certificates/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for specific certificate")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPATCH_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates/{uuid}"]
		req := httptest.NewRequest(http.MethodPatch, "/api/security/certificates/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH should be allowed for specific certificate")
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/security/certificates/{uuid}"]
		req := httptest.NewRequest(http.MethodDelete, "/api/security/certificates/550e8400-e29b-41d4-a716-446655440000", nil)

		action := rule.GetAction(req)

		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "DELETE should be denied for specific certificate")
		assert.NotEmpty(t, reason)
	})
}

func TestS3ProtocolsBucketsRules(t *testing.T) {
	t.Run("RulesRegistered", func(t *testing.T) {
		rules := GetProxyRules()
		_, ok := rules["/api/protocols/s3/buckets"]
		assert.True(t, ok)
		_, ok = rules["/api/protocols/s3/services/{uuid}/buckets"]
		assert.True(t, ok)
	})

	t.Run("WhenPOSTBucketsWithNasAndPath_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/protocols/s3/buckets"]
		body := bytes.NewBufferString(`{"name":"b1","type":"nas","nas_path":"/","svm":{"uuid":"550e8400-e29b-41d4-a716-446655440000"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/protocols/s3/buckets", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPOSTBucketsWithTypeS3_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/protocols/s3/buckets"]
		body := bytes.NewBufferString(`{"name":"b1","type":"s3","svm":{"uuid":"550e8400-e29b-41d4-a716-446655440000"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/protocols/s3/buckets", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTBucketsWithNasAndEmptyNasPath_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/protocols/s3/buckets"]
		body := bytes.NewBufferString(`{"name":"b1","type":"nas","nas_path":"","svm":{"uuid":"550e8400-e29b-41d4-a716-446655440000"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/protocols/s3/buckets", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPOSTBucketsWithNasAndNoNasPathKey_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/protocols/s3/buckets"]
		body := bytes.NewBufferString(`{"name":"b1","type":"nas","svm":{"uuid":"550e8400-e29b-41d4-a716-446655440000"}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/protocols/s3/buckets", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, reason)
		assert.Contains(t, reason, "nas_path")
	})

	t.Run("WhenPOSTServiceBucketsWithNasAndPath_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/protocols/s3/services/{uuid}/buckets"]
		body := bytes.NewBufferString(`{"name":"b1","type":"nas","nas_path":"/data"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/protocols/s3/services/550e8400-e29b-41d4-a716-446655440000/buckets", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPOSTServiceBucketsWithNasAndNoNasPathKey_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/protocols/s3/services/{uuid}/buckets"]
		body := bytes.NewBufferString(`{"name":"b1","type":"nas"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/protocols/s3/services/550e8400-e29b-41d4-a716-446655440000/buckets", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, reason)
		assert.Contains(t, reason, "nas_path")
	})

	t.Run("WhenGETBuckets_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules["/api/protocols/s3/buckets"]
		req := httptest.NewRequest(http.MethodGet, "/api/protocols/s3/buckets", nil)
		action := rule.GetAction(req)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed)
	})
}

func TestSVMCollectionRule(t *testing.T) {
	path := "/api/svm/svms"

	t.Run("RulesRegistered", func(t *testing.T) {
		rules := GetProxyRules()
		_, ok := rules[path]
		assert.True(t, ok, "Should have rule for /api/svm/svms")
	})

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodGet, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"name":"new-svm"}`)
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.Contains(t, reason, "SVM creation not allowed")
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodPatch, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.False(t, allowed)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.False(t, allowed)
	})
}

func TestSVMCollectionAliasRule(t *testing.T) {
	path := "/api/svms"

	t.Run("RulesRegistered", func(t *testing.T) {
		rules := GetProxyRules()
		_, ok := rules[path]
		assert.True(t, ok, "Should have rule for /api/svms alias")
	})

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodGet, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"name":"new-svm"}`)
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.Contains(t, reason, "SVM creation not allowed")
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodPatch, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.False(t, allowed)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.False(t, allowed)
	})
}

func TestSVMInstanceRule(t *testing.T) {
	path := "/api/svm/svms/{uuid}"

	t.Run("RulesRegistered", func(t *testing.T) {
		rules := GetProxyRules()
		_, ok := rules[path]
		assert.True(t, ok, "Should have rule for /api/svm/svms/{uuid}")
	})

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodGet, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPATCHWithAllowedFields_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"language": "en.UTF-8"}`)
		req := httptest.NewRequest(http.MethodPatch, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPATCHWithSnapshotPolicy_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"snapshot_policy": {"name": "custom1"}}`)
		req := httptest.NewRequest(http.MethodPatch, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPATCHWithBothAllowedFields_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"language": "C.UTF-8", "snapshot_policy": {"name": "default"}}`)
		req := httptest.NewRequest(http.MethodPatch, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPATCHWithNsswitch_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"nsswitch": {"group": ["files","nis"], "passwd": ["files","nis"], "netgroup": ["files","nis"], "namemap": ["files"]}}`)
		req := httptest.NewRequest(http.MethodPatch, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPATCHWithDisallowedField_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"name": "new-name", "language": "en.UTF-8"}`)
		req := httptest.NewRequest(http.MethodPatch, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.Contains(t, reason, "name")
		assert.Contains(t, reason, "not allowed")
	})

	t.Run("WhenPATCHWithStateField_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"state": "stopped"}`)
		req := httptest.NewRequest(http.MethodPatch, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.Contains(t, reason, "state")
		assert.Contains(t, reason, "not allowed")
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodPost, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.False(t, allowed)
	})

	t.Run("WhenDELETE_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.Contains(t, reason, "SVM deletion not allowed")
	})
}

func TestStorageLUNsRule(t *testing.T) {
	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns"]
		req := httptest.NewRequest(http.MethodGet, "/api/storage/luns", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for LUN listing")
	})

	t.Run("WhenPOSTWithoutSpaceReserve_ShouldAllow", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns"]
		body := bytes.NewBufferString(`{"name":"/vol/v1/lun1","space":{"size":1073741824}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/luns", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST without space.guarantee.requested should be allowed; reason: %s", reason)
	})

	t.Run("WhenPOSTWithThinProvisioning_ShouldAllow", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns"]
		body := bytes.NewBufferString(`{"name":"/vol/v1/lun1","space":{"guarantee":{"requested":false}}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/luns", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "POST with space.guarantee.requested=false should be allowed; reason: %s", reason)
	})

	t.Run("WhenPOSTWithThickProvisioning_ShouldDeny", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns"]
		body := bytes.NewBufferString(`{"name":"/vol/v1/lun1","space":{"guarantee":{"requested":true}}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/luns", body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "POST with space.guarantee.requested=true (thick) should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTOmittingSpaceReserve_ShouldForceThinProvisioning", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns"]
		body := bytes.NewBufferString(`{"name":"/vol/v1/lun1","space":{"size":1073741824}}`)
		req := httptest.NewRequest(http.MethodPost, "/api/storage/luns", body)
		req.Header.Set("Content-Type", "application/json")

		req = dsl.ParseRequestBody(req)
		resolved, allowed, reason := dsl.ResolveAction(rule.GetAction(req), req)
		assert.True(t, allowed, reason)

		_, err := resolved.ProcessRequest(req, nil)
		assert.NoError(t, err)

		out, err := io.ReadAll(req.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(out), `"requested":false`, "proxy should force thin provisioning when space-reserve is omitted")
		assert.Contains(t, string(out), `"scsi_thin_provisioning_support_enabled":true`, "proxy should enable SCSI thin provisioning support on LUN creation")
	})

	t.Run("WhenPATCH_ShouldDeny", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns"]
		req := httptest.NewRequest(http.MethodPatch, "/api/storage/luns", nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH should be denied for LUN collection")
	})
}

func TestStorageLUNsUUIDRule(t *testing.T) {
	const lunPath = "/api/storage/luns/550e8400-e29b-41d4-a716-446655440000"

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns/{uuid}"]
		req := httptest.NewRequest(http.MethodGet, lunPath, nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "GET should be allowed for specific LUN")
	})

	t.Run("WhenPATCHWithUnrelatedField_ShouldAllow", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns/{uuid}"]
		body := bytes.NewBufferString(`{"space":{"size":2147483648}}`)
		req := httptest.NewRequest(http.MethodPatch, lunPath, body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, "PATCH that does not touch space reservation should be allowed; reason: %s", reason)
	})

	t.Run("WhenPATCHReEnablingSpaceReserve_ShouldDeny", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns/{uuid}"]
		body := bytes.NewBufferString(`{"space":{"guarantee":{"requested":true}}}`)
		req := httptest.NewRequest(http.MethodPatch, lunPath, body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, "PATCH re-enabling space reservation (thick) should be denied")
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenDELETE_ShouldAllow", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns/{uuid}"]
		req := httptest.NewRequest(http.MethodDelete, lunPath, nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.True(t, allowed, "DELETE should be allowed for specific LUN")
	})

	t.Run("WhenPOST_ShouldDeny", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/luns/{uuid}"]
		req := httptest.NewRequest(http.MethodPost, lunPath, nil)

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, _ := action.ShouldAllow(req)
		assert.False(t, allowed, "POST should be denied for specific LUN")
	})
}

func TestPrivateCliObjectStoreBucketCreateRule(t *testing.T) {
	path := "/api/private/cli/vserver/object-store-server/bucket"

	t.Run("RulesRegistered", func(t *testing.T) {
		rules := GetProxyRules()
		_, ok := rules[path]
		assert.True(t, ok)
	})

	t.Run("WhenPOSTWithNasAndPath_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"vserver":"vs1","bucket":"b1","type":"nas","nas_path":"/data"}`)
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPOSTWithTypeS3_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"vserver":"vs1","bucket":"b1","type":"s3"}`)
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed)
		assert.NotEmpty(t, reason)
	})

	t.Run("WhenPOSTWithNasAndEmptyNasPath_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"vserver":"vs1","bucket":"b1","type":"nas","nas_path":""}`)
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPOSTWithNasAndNoNasPathKey_ShouldDeny", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"vserver":"vs1","bucket":"b1","type":"nas"}`)
		req := httptest.NewRequest(http.MethodPost, path, body)
		req.Header.Set("Content-Type", "application/json")

		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.False(t, allowed, reason)
		assert.Contains(t, reason, "nas_path")
	})

	t.Run("WhenGET_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodGet, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenPATCH_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		body := bytes.NewBufferString(`{"type":"nas","nas_path":"/vol"}`)
		req := httptest.NewRequest(http.MethodPatch, path, body)
		req.Header.Set("Content-Type", "application/json")
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})

	t.Run("WhenDELETE_ShouldAllow", func(t *testing.T) {
		rules := GetProxyRules()
		rule := rules[path]
		req := httptest.NewRequest(http.MethodDelete, path, nil)
		action := rule.GetAction(req)
		assert.NotNil(t, action)
		allowed, reason := action.ShouldAllow(req)
		assert.True(t, allowed, reason)
	})
}

func TestStorageVolumesResponseFiltering_AllowsTieringFootprint(t *testing.T) {
	// The volume GET response must expose the tiering footprint fields while still stripping
	// the other footprint/efficiency fields. Corresponds to the RemoveFields list on
	// GET /api/storage/volumes in rule_map.go.
	const body = `{` +
		`"efficiency":{"savings":10},` +
		`"space":{` +
		`"size":1073741824,` +
		`"physical_used":100,` +
		`"used":900,` +
		`"percent_used":5,` +
		`"footprint":200,` +
		`"effective_total_footprint":300,` +
		`"total_metadata_footprint":400,` +
		`"total_footprint":500,` +
		`"local_tier_footprint":600,` +
		`"capacity_tier_footprint":700,` +
		`"performance_tier_footprint":800,` +
		`"logical_space":{"used":700,"used_percent":70,"enforcement":true,"reporting":true}` +
		`}}`

	makeResp := func(b string) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(b)),
		}
	}

	t.Run("WhenVolumeListing_ShouldRetainTieringFootprintAndStripOthers", func(t *testing.T) {
		// Real ONTAP GET /api/storage/volumes returns a records[] envelope; RemoveFields must
		// apply per-record so tiering fields survive inside records[*].space while the rest
		// (efficiency, physical_used, footprint, etc.) are stripped from every record.
		const listBody = `{"records":[` +
			`{"name":"vol1","space":{"size":1073741824,"physical_used":100,` +
			`"used":900,"footprint":200,` +
			`"effective_total_footprint":300,"total_metadata_footprint":400,"total_footprint":500,` +
			`"local_tier_footprint":600,"capacity_tier_footprint":700,"performance_tier_footprint":800,` +
			`"logical_space":{"used":700,"enforcement":true,"reporting":true}},` +
			`"efficiency":{"savings":10}},` +
			`{"name":"vol2","space":{"size":2147483648,"physical_used":101,` +
			`"used":901,"footprint":201,` +
			`"effective_total_footprint":301,"total_metadata_footprint":401,"total_footprint":501,` +
			`"local_tier_footprint":601,"capacity_tier_footprint":701,"performance_tier_footprint":801,` +
			`"logical_space":{"used":701,"enforcement":true,"reporting":true}},` +
			`"efficiency":{"savings":11}}` +
			`],"num_records":2}`

		rule := GetProxyRules()["/api/storage/volumes"]
		resp := makeResp(listBody)

		_, err := rule.GET.ProcessResponse(resp)
		assert.NoError(t, err)

		out, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		got := string(out)

		// Tiering footprint fields are allowed through inside each record's space.
		for _, want := range []string{
			`"total_footprint":500`,
			`"capacity_tier_footprint":700`, `"performance_tier_footprint":800`,
			`"total_footprint":501`,
			`"capacity_tier_footprint":701`, `"performance_tier_footprint":801`,
		} {
			assert.Contains(t, got, want)
		}

		// Other footprint/efficiency fields remain stripped from every record
		// (local_tier_footprint is in the RemoveFields deny-list, so it is stripped too).
		assert.NotContains(t, got, `"efficiency"`)
		assert.NotContains(t, got, `"physical_used"`)
		assert.NotContains(t, got, `"footprint":`)
		assert.NotContains(t, got, `"local_tier_footprint"`)
		assert.NotContains(t, got, `"effective_total_footprint"`)
		assert.NotContains(t, got, `"total_metadata_footprint"`)

		// The pre-efficiency space.used is stripped so customers only see the logical
		// (df-like) figure. space.logical_space.used must survive.
		assert.NotContains(t, got, `"used":900`)
		assert.NotContains(t, got, `"used":901`)
		assert.Contains(t, got, `"used":700`)
		assert.Contains(t, got, `"used":701`)
	})

	t.Run("WhenSpecificVolume_ShouldRetainTieringFootprintAndStripOthers", func(t *testing.T) {
		rule := GetProxyRules()["/api/storage/volumes/{uuid}"]
		resp := makeResp(body)

		_, err := rule.GET.ProcessResponse(resp)
		assert.NoError(t, err)

		out, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		got := string(out)

		assert.Contains(t, got, `"total_footprint":500`)
		assert.Contains(t, got, `"capacity_tier_footprint":700`)
		assert.Contains(t, got, `"performance_tier_footprint":800`)

		assert.NotContains(t, got, `"efficiency"`)
		assert.NotContains(t, got, `"footprint":`)
		assert.NotContains(t, got, `"local_tier_footprint"`)
		assert.NotContains(t, got, `"effective_total_footprint"`)

		// The pre-efficiency space.used and space.percent_used are stripped; logical used and
		// logical used_percent survive.
		assert.NotContains(t, got, `"used":900`)
		assert.NotContains(t, got, `"percent_used"`)
		assert.Contains(t, got, `"used":700`)
		assert.Contains(t, got, `"used_percent":70`)
	})
}

// volOntapResp is a representative ONTAP volume object carrying the pre-efficiency space.used
// (physical footprint) plus tiering footprints and logical_space.used. Reused by the
// create/update/delete response-filtering tests.
const volOntapResp = `{"name":"vol1","space":{"size":1073741824,"physical_used":100,"used":900,"percent_used":5,` +
	`"footprint":200,"local_tier_footprint":600,"capacity_tier_footprint":700,` +
	`"performance_tier_footprint":800,"total_footprint":500,` +
	`"logical_space":{"used":700,"used_percent":70,"enforcement":true,"reporting":true}},"efficiency":{"savings":10}}`

func makeRespWithReq(body string, req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Request:    req,
	}
}

func assertVolumeSpaceUsedStripped(t *testing.T, got string) {
	t.Helper()
	// Pre-efficiency physical fields stripped.
	assert.NotContains(t, got, `"used":900`, "space.used (physical) must be stripped")
	assert.NotContains(t, got, `"percent_used"`, "space.percent_used (pre-efficiency %) must be stripped")
	assert.NotContains(t, got, `"physical_used"`)
	assert.NotContains(t, got, `"efficiency"`)
	assert.NotContains(t, got, `"footprint":`)
	assert.NotContains(t, got, `"local_tier_footprint"`)
	// Logical (df-like) figure/percent and tiering footprints retained.
	assert.Contains(t, got, `"used":700`, "logical_space.used must survive")
	assert.Contains(t, got, `"used_percent":70`, "logical_space.used_percent must survive")
	assert.Contains(t, got, `"capacity_tier_footprint":700`)
	assert.Contains(t, got, `"total_footprint":500`)
}

// TestVolumeResponseFiltering_MutatingAndPrivateCLI exercises ProcessResponse on the volume
// create/update/delete responses and the private-CLI volume GET. GET collection + by-uuid are
// already covered by TestStorageVolumesResponseFiltering_AllowsTieringFootprint; these close the
// gap so a missed $.space.used (or $.used) on those endpoints fails CI.
func TestVolumeResponseFiltering_MutatingAndPrivateCLI(t *testing.T) {
	t.Run("WhenVolumeCreate_ShouldStripSpaceUsed", func(t *testing.T) {
		orig := validateVolumeCreation
		defer func() { validateVolumeCreation = orig }()
		validateVolumeCreation = func(r *http.Request) (bool, string) { return true, "" }

		req := httptest.NewRequest(http.MethodPost, "/api/storage/volumes",
			bytes.NewBufferString(`{"name":"vol1","size":1073741824}`))
		req.Header.Set("Content-Type", "application/json")
		resp := makeRespWithReq(volOntapResp, req)

		_, err := GetProxyRules()["/api/storage/volumes"].POST.ProcessResponse(resp)
		assert.NoError(t, err)
		out, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assertVolumeSpaceUsedStripped(t, string(out))
	})

	t.Run("WhenVolumeUpdate_ShouldStripSpaceUsed", func(t *testing.T) {
		orig := validateVolumeModification
		defer func() { validateVolumeModification = orig }()
		validateVolumeModification = func(r *http.Request) (bool, string) { return true, "" }

		req := httptest.NewRequest(http.MethodPatch, "/api/storage/volumes/uuid-1",
			bytes.NewBufferString(`{"size":2147483648}`))
		req.Header.Set("Content-Type", "application/json")
		resp := makeRespWithReq(volOntapResp, req)

		_, err := GetProxyRules()["/api/storage/volumes/{uuid}"].PATCH.ProcessResponse(resp)
		assert.NoError(t, err)
		out, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assertVolumeSpaceUsedStripped(t, string(out))
	})

	t.Run("WhenVolumeDelete_ShouldStripSpaceUsed", func(t *testing.T) {
		orig := validateVolumeDeletion
		defer func() { validateVolumeDeletion = orig }()
		validateVolumeDeletion = func(r *http.Request) (bool, string) { return true, "" }

		req := httptest.NewRequest(http.MethodDelete, "/api/storage/volumes/uuid-1", nil)
		resp := makeRespWithReq(volOntapResp, req)

		_, err := GetProxyRules()["/api/storage/volumes/{uuid}"].DELETE.ProcessResponse(resp)
		assert.NoError(t, err)
		out, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assertVolumeSpaceUsedStripped(t, string(out))
	})

	t.Run("WhenPrivateCLIVolumeGET_ShouldStripFlatUsed", func(t *testing.T) {
		// Private CLI uses a flat (non-$.space) shape; $.used is the pre-efficiency figure.
		const cliResp = `{"records":[{"volume":"vol1","size":1073741824,"used":900,` +
			`"physical_used":100,"percent_used":42,"overprovisioned":true}],"num_records":1}`
		req := httptest.NewRequest(http.MethodGet, "/api/private/cli/volume", nil)
		resp := makeRespWithReq(cliResp, req)

		_, err := GetProxyRules()["/api/private/cli/volume"].GET.ProcessResponse(resp)
		assert.NoError(t, err)
		out, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		got := string(out)

		assert.NotContains(t, got, `"used":900`, "private CLI $.used (physical) must be stripped")
		assert.NotContains(t, got, `"physical_used"`)
		assert.NotContains(t, got, `"percent_used"`)
		assert.NotContains(t, got, `"overprovisioned"`)
		// Non-deny-listed fields survive.
		assert.Contains(t, got, `"volume":"vol1"`)
		assert.Contains(t, got, `"size":1073741824`)
	})
}

// TestVolumeRemoveFieldsSingleSource guards the DRY invariant: every volume ModifyResponse must
// reference the shared field-list vars, so the pre-efficiency space.used ($.used for private CLI)
// deny-list can never silently diverge across the create/update/delete/GET endpoints.
func TestVolumeRemoveFieldsSingleSource(t *testing.T) {
	rules := GetProxyRules()

	removeFieldsOf := func(t *testing.T, a dsl.IAction) []string {
		t.Helper()
		switch v := a.(type) {
		case dsl.Allow:
			rf, ok := v.ModifyResponse.(dsl.RemoveFields)
			assert.Truef(t, ok, "expected RemoveFields ModifyResponse, got %T", v.ModifyResponse)
			return rf.Fields
		case dsl.When:
			al, ok := v.IsTrue.(dsl.Allow)
			assert.Truef(t, ok, "expected When.IsTrue to be Allow, got %T", v.IsTrue)
			rf, ok := al.ModifyResponse.(dsl.RemoveFields)
			assert.Truef(t, ok, "expected RemoveFields ModifyResponse, got %T", al.ModifyResponse)
			return rf.Fields
		default:
			t.Fatalf("unexpected action type %T", a)
			return nil
		}
	}

	// GET (list + by-uuid) use the list variant (base + logical_space config toggles).
	assert.Equal(t, volumeListSpaceRemoveFields, removeFieldsOf(t, rules["/api/storage/volumes"].GET))
	assert.Equal(t, volumeListSpaceRemoveFields, removeFieldsOf(t, rules["/api/storage/volumes/{uuid}"].GET))
	// create/update/delete use the base variant.
	assert.Equal(t, volumeSpaceRemoveFields, removeFieldsOf(t, rules["/api/storage/volumes"].POST))
	assert.Equal(t, volumeSpaceRemoveFields, removeFieldsOf(t, rules["/api/storage/volumes/{uuid}"].PATCH))
	assert.Equal(t, volumeSpaceRemoveFields, removeFieldsOf(t, rules["/api/storage/volumes/{uuid}"].DELETE))
	// private CLI GET uses the flat variant.
	assert.Equal(t, privateCLIVolumeRemoveFields, removeFieldsOf(t, rules["/api/private/cli/volume"].GET))

	// Critical invariants regardless of future edits.
	assert.Contains(t, volumeSpaceRemoveFields, "$.space.used", "physical space.used must stay stripped")
	assert.Contains(t, volumeSpaceRemoveFields, "$.space.percent_used", "pre-efficiency space.percent_used must stay stripped")
	assert.Contains(t, privateCLIVolumeRemoveFields, "$.used", "private CLI physical used must stay stripped")
	assert.Contains(t, privateCLIVolumeRemoveFields, "$.percent_used", "private CLI physical percent_used must stay stripped")
	assert.NotContains(t, volumeListSpaceRemoveFields, "$.space.logical_space.used", "logical used must never be stripped")
	assert.NotContains(t, volumeListSpaceRemoveFields, "$.space.logical_space.used_percent", "logical used_percent must never be stripped")
}

func TestSystemNodeRunRule_Blocked(t *testing.T) {
	paths := []string{
		"/api/private/cli/system/node/run",
		"/api/private/cli/run",
	}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}

	for _, path := range paths {
		rules := GetProxyRules()
		rule, ok := rules[path]
		assert.Truef(t, ok, "Should have a rule for %s", path)

		for _, method := range methods {
			req := httptest.NewRequest(method, path, nil)
			action := rule.GetAction(req)
			assert.NotNilf(t, action, "%s %s should have an action", method, path)

			allowed, reason := action.ShouldAllow(req)
			assert.Falsef(t, allowed, "%s %s should be denied", method, path)
			assert.NotEmptyf(t, reason, "%s %s deny should carry a reason", method, path)
		}
	}
}
