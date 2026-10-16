package opsinventory

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type M = map[string]any

func TestGetVolume(t *testing.T) {
	e := newEnv(t)

	t.Run("by uuid without project", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "identity": M{"uuid": "vol-1"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.Equal(t, "volume", r.body["resourceType"])
		assert.Equal(t, "vcp_db", r.body["source"])
		assert.Equal(t, "v1", r.body["name"])
		assert.Equal(t, "1111", r.body["projectNumber"])
		assert.Equal(t, "pool-1", r.body["poolUuid"])
		assert.Equal(t, "svm-1", r.body["svmUuid"])
		assert.Equal(t, "vault-1", r.body["backupVaultUuid"])
		assert.Equal(t, datamodelAvailableDetails, r.body["stateDetails"])
		assert.Contains(t, r.body, "deletedAt")
		assert.Nil(t, r.body["deletedAt"], "live rows carry deletedAt: null")
		assert.NotContains(t, r.body, "id", "internal id is never returned")
	})

	t.Run("deleted row is returned with deletedAt", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "identity": M{"uuid": "vol-del"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.NotNil(t, r.body["deletedAt"])
	})

	t.Run("transitional state is returned as stored", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "identity": M{"uuid": "vol-2"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.Equal(t, "CREATING", r.body["state"])
	})

	t.Run("by name needs pool parent", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "projectNumber": "1111", "identity": M{"name": "v1"}})
		assert.Equal(t, http.StatusBadRequest, r.status, r.raw)
		assert.Contains(t, r.body["message"], "needs parent pool")
	})

	t.Run("by name prefers the live row", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "projectNumber": "1111", "identity": M{"name": "v1"}, "parents": []M{{"type": "pool", "id": "pool-1"}}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.Equal(t, "vol-1", r.body["uuid"])
	})

	t.Run("by name needs project", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "identity": M{"name": "v1"}, "parents": []M{{"type": "pool", "id": "pool-1"}}})
		assert.Equal(t, http.StatusBadRequest, r.status, r.raw)
	})

	t.Run("by name with a foreign pool is 404", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "projectNumber": "1111", "identity": M{"name": "v1"}, "parents": []M{{"type": "pool", "id": "pool-other"}}})
		assert.Equal(t, http.StatusNotFound, r.status, r.raw)
	})

	t.Run("ambiguous live name is 409", func(t *testing.T) {
		dup := *e.fx.vol2
		dup.ID, dup.UUID, dup.Name = 0, "vol-dup", "v2"
		require.NoError(t, e.db.Create(&dup).Error)
		r := e.get(M{"resourceType": "volume", "projectNumber": "1111", "identity": M{"name": "v2"}, "parents": []M{{"type": "pool", "id": "pool-1"}}})
		assert.Equal(t, http.StatusConflict, r.status, r.raw)
	})

	t.Run("uuid with a different project is 404", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "projectNumber": "2222", "identity": M{"uuid": "vol-1"}})
		assert.Equal(t, http.StatusNotFound, r.status, r.raw)
	})

	t.Run("unknown project is 404 project not found", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "projectNumber": "9999", "identity": M{"uuid": "vol-1"}})
		assert.Equal(t, http.StatusNotFound, r.status, r.raw)
		assert.Equal(t, "project not found", r.body["message"])
	})

	t.Run("missing row is 404", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "identity": M{"uuid": "nope"}})
		assert.Equal(t, http.StatusNotFound, r.status, r.raw)
	})
}

const datamodelAvailableDetails = "Available for use"

func TestGetValidation(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		name   string
		body   any
		status int
	}{
		{"both uuid and name", M{"resourceType": "volume", "identity": M{"uuid": "a", "name": "b"}}, 400},
		{"neither uuid nor name", M{"resourceType": "volume", "identity": M{}}, 400},
		{"parents with uuid", M{"resourceType": "volume", "identity": M{"uuid": "vol-1"}, "parents": []M{{"type": "pool", "id": "pool-1"}}}, 400},
		{"cmek by name", M{"resourceType": "cmek", "projectNumber": "1111", "identity": M{"name": "x"}}, 400},
		{"job by name", M{"resourceType": "job", "projectNumber": "1111", "identity": M{"name": "x"}}, 400},
		{"wrong parent for name", M{"resourceType": "volume", "projectNumber": "1111", "identity": M{"name": "v1"}, "parents": []M{{"type": "svm", "id": "svm-1"}}}, 400},
		{"unknown resource type", M{"resourceType": "disk", "identity": M{"uuid": "x"}}, 400},
		{"project id instead of number", M{"resourceType": "volume", "projectNumber": "my-team-prod", "identity": M{"uuid": "vol-1"}}, 400},
		{"unknown top-level field", M{"resourceType": "volume", "identity": M{"uuid": "vol-1"}, "includeDeleted": true}, 400},
		{"unknown identity field", M{"resourceType": "volume", "identity": M{"uuid": "vol-1", "id": 1}}, 400},
		{"malformed json", `{"resourceType":`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.get(tc.body)
			assert.Equal(t, tc.status, r.status, r.raw)
		})
	}
}

func TestGetEveryType(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct{ resourceType, uuid string }{
		{"pool", "pool-1"}, {"pool", "pool-del"}, {"volume", "vol-1"}, {"snapshot", "snap-1"}, {"replication", "rep-1"},
		{"backup", "bk-1"}, {"backupVault", "vault-1"}, {"backupPolicy", "bp-1"}, {"cmek", "kms-1"},
		{"activeDirectory", "ad-1"}, {"job", "job-1"}, {"job", "job-admin"},
	} {
		t.Run(tc.resourceType+"/"+tc.uuid, func(t *testing.T) {
			r := e.get(M{"resourceType": tc.resourceType, "identity": M{"uuid": tc.uuid}})
			require.Equal(t, http.StatusOK, r.status, r.raw)
			assert.Equal(t, tc.resourceType, r.body["resourceType"])
		})
	}

	t.Run("pool returns safe credential metadata only", func(t *testing.T) {
		r := e.get(M{"resourceType": "pool", "identity": M{"uuid": "pool-1"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.Equal(t, float64(2), r.body["authType"])
		assert.Equal(t, true, r.body["credentialConfigured"])
		assert.Equal(t, "kms-1", r.body["kmsConfigUuid"])
		assert.Equal(t, "ad-1", r.body["activeDirectoryUuid"])
		assert.Equal(t, true, r.body["stateDetailsWithheld"], "free-text state details are withheld")
		assert.NotContains(t, r.body, "stateDetails")
	})

	t.Run("cmek carries its service account and key rotation", func(t *testing.T) {
		r := e.get(M{"resourceType": "cmek", "identity": M{"uuid": "kms-1"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		sa := r.body["serviceAccount"].(map[string]any)
		assert.Equal(t, "sa-1", sa["uuid"])
		assert.Equal(t, "vcp-sa@example.iam.gserviceaccount.com", sa["email"])
		rotation := sa["keyRotation"].(map[string]any)
		assert.Equal(t, "key-new", rotation["primaryKeyId"])
		assert.Equal(t, "2026-09-01T00:00:00Z", rotation["primaryKeyCreatedAt"])
		assert.Equal(t, true, rotation["rotationInProgress"])
		assert.NotContains(t, r.raw, sentinel)
	})

	t.Run("backup project comes from its vault", func(t *testing.T) {
		r := e.get(M{"resourceType": "backup", "identity": M{"uuid": "bk-2"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.Equal(t, "1111", r.body["projectNumber"])
		assert.Equal(t, "vault-1", r.body["backupVaultUuid"])
	})

	t.Run("active directory project is resolved by id", func(t *testing.T) {
		r := e.get(M{"resourceType": "activeDirectory", "identity": M{"uuid": "ad-1"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.Equal(t, "1111", r.body["projectNumber"])
		assert.Len(t, r.body["serverRootCaCertificateSha256"], 64)
	})

	t.Run("job error is the catalog message, not raw details", func(t *testing.T) {
		r := e.get(M{"resourceType": "job", "identity": M{"uuid": "job-1"}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		job := r.body["job"].(map[string]any)
		assert.Equal(t, float64(1003), job["trackingId"])
		assert.Equal(t, "Resource not found", job["errorMessage"])
		assert.Equal(t, "1111", job["projectNumber"])
	})

	t.Run("get by name for the other types", func(t *testing.T) {
		for _, body := range []M{
			{"resourceType": "pool", "projectNumber": "1111", "identity": M{"name": "p1"}},
			{"resourceType": "snapshot", "projectNumber": "1111", "identity": M{"name": "s1"}, "parents": []M{{"type": "volume", "id": "vol-del"}}},
			{"resourceType": "replication", "projectNumber": "1111", "identity": M{"name": "r1"}, "parents": []M{{"type": "volume", "id": "vol-1"}}},
			{"resourceType": "backup", "projectNumber": "1111", "identity": M{"name": "b1"}, "parents": []M{{"type": "backupVault", "id": "vault-1"}}},
			{"resourceType": "backupVault", "projectNumber": "1111", "identity": M{"name": "bv1"}},
			{"resourceType": "backupPolicy", "projectNumber": "1111", "identity": M{"name": "bp1"}},
			{"resourceType": "activeDirectory", "projectNumber": "1111", "identity": M{"name": "ad1"}},
		} {
			r := e.get(body)
			assert.Equal(t, http.StatusOK, r.status, "%v: %s", body, r.raw)
		}
	})
}

func TestListVolumesAndPaging(t *testing.T) {
	e := newEnv(t)

	t.Run("hides deleted rows by default", func(t *testing.T) {
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111"})
		assert.Equal(t, []string{"vol-1", "vol-2"}, uuidsOf(resources(t, r)))
		assert.Equal(t, "", r.body["nextPageToken"])
	})

	t.Run("includeDeleted", func(t *testing.T) {
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "includeDeleted": true})
		assert.Equal(t, []string{"vol-1", "vol-2", "vol-del"}, uuidsOf(resources(t, r)))
	})

	t.Run("pages with a token", func(t *testing.T) {
		req := M{"resourceType": "volume", "projectNumber": "1111", "includeDeleted": true, "pageSize": 2}
		page1 := e.list(req)
		assert.Equal(t, []string{"vol-1", "vol-2"}, uuidsOf(resources(t, page1)))
		token, _ := page1.body["nextPageToken"].(string)
		require.NotEmpty(t, token)

		req["pageToken"] = token
		page2 := e.list(req)
		assert.Equal(t, []string{"vol-del"}, uuidsOf(resources(t, page2)))
		assert.Equal(t, "", page2.body["nextPageToken"])

		t.Run("token with other filters is rejected", func(t *testing.T) {
			r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "pageSize": 2, "pageToken": token})
			assert.Equal(t, http.StatusBadRequest, r.status, r.raw)
			assert.Contains(t, r.body["message"], "does not match")
		})
	})

	t.Run("bad token", func(t *testing.T) {
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "pageToken": "!!!"})
		assert.Equal(t, http.StatusBadRequest, r.status, r.raw)
	})

	t.Run("pageSize above 50 is clamped", func(t *testing.T) {
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "pageSize": 5000})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		assert.Equal(t, 50, e.logs.last(t)["page_size"])
	})

	t.Run("default pageSize is 10", func(t *testing.T) {
		resources(t, e.list(M{"resourceType": "volume", "projectNumber": "1111"}))
		assert.Equal(t, 10, e.logs.last(t)["page_size"])
	})

	t.Run("filters", func(t *testing.T) {
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "filters": M{"state": "CREATING"}})
		assert.Equal(t, []string{"vol-2"}, uuidsOf(resources(t, r)))
		r = e.list(M{"resourceType": "volume", "projectNumber": "1111", "includeDeleted": true, "filters": M{"name": "v1"}})
		assert.Equal(t, []string{"vol-1", "vol-del"}, uuidsOf(resources(t, r)))
	})

	t.Run("time filter is half-open", func(t *testing.T) {
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "includeDeleted": true,
			"timeFilters": []M{{"field": "createdAt", "from": "2026-10-02T00:00:00Z", "to": "2026-10-03T00:00:00Z"}}})
		assert.Equal(t, []string{"vol-2"}, uuidsOf(resources(t, r)))
		r = e.list(M{"resourceType": "volume", "projectNumber": "1111", "includeDeleted": true,
			"timeFilters": []M{{"field": "createdAt", "from": "2026-10-03T00:00:00+00:00"}}})
		assert.Equal(t, []string{"vol-del"}, uuidsOf(resources(t, r)))
	})

	t.Run("parents", func(t *testing.T) {
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "svm", "id": "svm-1"}}})
		assert.Equal(t, []string{"vol-1"}, uuidsOf(resources(t, r)))
		r = e.list(M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "backupVault", "id": "vault-1"}, {"type": "pool", "id": "pool-1"}}})
		assert.Equal(t, []string{"vol-1"}, uuidsOf(resources(t, r)), "parents are AND-ed")
		r = e.list(M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "backupPolicy", "id": "bp-1"}}})
		assert.Equal(t, []string{"vol-1"}, uuidsOf(resources(t, r)))
	})
}

func TestListValidation(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		name   string
		body   any
		status int
	}{
		{"volume without project", M{"resourceType": "volume"}, 400},
		{"parent not allowed for type", M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "host", "id": "host-1"}}}, 400},
		{"same parent twice", M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "pool", "id": "pool-1"}, {"type": "pool", "id": "pool-del"}}}, 400},
		{"foreign parent", M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "pool", "id": "pool-other"}}}, 404},
		{"unknown parent", M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "pool", "id": "nope"}}}, 404},
		{"unknown project", M{"resourceType": "volume", "projectNumber": "9999"}, 404},
		{"unknown state", M{"resourceType": "volume", "projectNumber": "1111", "filters": M{"state": "HAPPY"}}, 400},
		{"name filter on cmek", M{"resourceType": "cmek", "projectNumber": "1111", "filters": M{"name": "x"}}, 400},
		{"name filter on job", M{"resourceType": "job", "projectNumber": "1111", "filters": M{"name": "x"}}, 400},
		{"correlationId on volume", M{"resourceType": "volume", "projectNumber": "1111", "filters": M{"correlationId": "x"}}, 400},
		{"unknown filter key", M{"resourceType": "volume", "projectNumber": "1111", "filters": M{"size": "1"}}, 400},
		{"from not before to", M{"resourceType": "volume", "projectNumber": "1111", "timeFilters": []M{{"field": "createdAt", "from": "2026-10-03T00:00:00Z", "to": "2026-10-03T00:00:00Z"}}}, 400},
		{"time filter without bounds", M{"resourceType": "volume", "projectNumber": "1111", "timeFilters": []M{{"field": "createdAt"}}}, 400},
		{"same time field twice", M{"resourceType": "volume", "projectNumber": "1111", "timeFilters": []M{{"field": "createdAt", "from": "2026-10-01T00:00:00Z"}, {"field": "createdAt", "to": "2026-10-09T00:00:00Z"}}}, 400},
		{"unknown time field", M{"resourceType": "volume", "projectNumber": "1111", "timeFilters": []M{{"field": "deletedAt", "from": "2026-10-01T00:00:00Z"}}}, 400},
		{"malformed timestamp", M{"resourceType": "volume", "projectNumber": "1111", "timeFilters": []M{{"field": "createdAt", "from": "yesterday"}}}, 400},
		{"negative pageSize", M{"resourceType": "volume", "projectNumber": "1111", "pageSize": -1}, 400},
		{"backup without vault", M{"resourceType": "backup", "projectNumber": "1111"}, 400},
		{"backup with only volume", M{"resourceType": "backup", "projectNumber": "1111", "parents": []M{{"type": "volume", "id": "vol-1"}}}, 400},
		{"job without project or filter", M{"resourceType": "job"}, 400},
		{"job without project, includeDeleted is not a filter", M{"resourceType": "job", "includeDeleted": true}, 400},
		{"job without project, empty correlationId is not a filter", M{"resourceType": "job", "filters": M{"correlationId": ""}}, 400},
		{"job without project, empty type is not a filter", M{"resourceType": "job", "filters": M{"type": ""}}, 400},
		{"empty name filter", M{"resourceType": "volume", "projectNumber": "1111", "filters": M{"name": ""}}, 400},
		{"empty state filter", M{"resourceType": "volume", "projectNumber": "1111", "filters": M{"state": ""}}, 400},
		{"empty parent id", M{"resourceType": "volume", "projectNumber": "1111", "parents": []M{{"type": "pool", "id": ""}}}, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.list(tc.body)
			assert.Equal(t, tc.status, r.status, r.raw)
		})
	}
}

func TestListEveryType(t *testing.T) {
	e := newEnv(t)
	for _, tc := range []struct {
		name string
		body M
		want []string
	}{
		{"pools", M{"resourceType": "pool", "projectNumber": "1111"}, []string{"pool-1"}},
		{"pools with deleted", M{"resourceType": "pool", "projectNumber": "1111", "includeDeleted": true}, []string{"pool-1", "pool-del"}},
		{"pools by cmek", M{"resourceType": "pool", "projectNumber": "1111", "parents": []M{{"type": "cmek", "id": "kms-1"}}}, []string{"pool-1"}},
		{"pools by host", M{"resourceType": "pool", "projectNumber": "1111", "parents": []M{{"type": "host", "id": "host-1"}}}, []string{"pool-1"}},
		{"pools by activeDirectory", M{"resourceType": "pool", "projectNumber": "1111", "parents": []M{{"type": "activeDirectory", "id": "ad-1"}}}, []string{"pool-1"}},
		{"snapshots of a deleted volume", M{"resourceType": "snapshot", "projectNumber": "1111", "includeDeleted": true, "parents": []M{{"type": "volume", "id": "vol-del"}}}, []string{"snap-1"}},
		{"replications", M{"resourceType": "replication", "projectNumber": "1111"}, []string{"rep-1"}},
		{"replications by remote source", M{"resourceType": "replication", "projectNumber": "1111", "parents": []M{{"type": "sourceVolume", "id": "remote-src"}}}, []string{"rep-1"}},
		{"replications by destination", M{"resourceType": "replication", "projectNumber": "1111", "parents": []M{{"type": "destinationVolume", "id": "vol-1"}}}, []string{"rep-1"}},
		{"backups in vault, any source project", M{"resourceType": "backup", "projectNumber": "1111", "parents": []M{{"type": "backupVault", "id": "vault-1"}}}, []string{"bk-1", "bk-2"}},
		{"backups in vault and volume", M{"resourceType": "backup", "projectNumber": "1111", "parents": []M{{"type": "backupVault", "id": "vault-1"}, {"type": "volume", "id": "vol-other"}}}, []string{"bk-2"}},
		{"backup vaults", M{"resourceType": "backupVault", "projectNumber": "1111", "filters": M{"state": "READY"}}, []string{"vault-1"}},
		{"backup policies", M{"resourceType": "backupPolicy", "projectNumber": "1111"}, []string{"bp-1"}},
		{"cmek", M{"resourceType": "cmek", "projectNumber": "1111"}, []string{"kms-1"}},
		{"active directories", M{"resourceType": "activeDirectory", "projectNumber": "1111"}, []string{"ad-1"}},
		{"jobs in project", M{"resourceType": "job", "projectNumber": "1111"}, []string{"job-1", "job-seq"}},
		{"jobs by correlation id, no project", M{"resourceType": "job", "filters": M{"correlationId": "corr-admin"}}, []string{"job-admin"}},
		{"jobs by type and state", M{"resourceType": "job", "filters": M{"type": "CREATE_VOLUME", "state": "NEW"}}, []string{"job-seq"}},
		{"jobs by resource", M{"resourceType": "job", "parents": []M{{"type": "resource", "id": "none"}}}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := e.list(tc.body)
			assert.Equal(t, tc.want, uuidsOf(resources(t, r)))
		})
	}

	t.Run("backup vault of another project is 404", func(t *testing.T) {
		r := e.list(M{"resourceType": "backup", "projectNumber": "1111", "parents": []M{{"type": "backupVault", "id": "vault-2"}}})
		assert.Equal(t, http.StatusNotFound, r.status, r.raw)
	})

	t.Run("list jobs carry SKIPPED temporal status and no describe", func(t *testing.T) {
		r := e.list(M{"resourceType": "job", "projectNumber": "1111"})
		for _, item := range resources(t, r) {
			assert.Equal(t, "SKIPPED", item["temporal"].(map[string]any)["enrichmentStatus"])
			assert.Equal(t, "vcp_db", item["source"])
		}
		assert.Empty(t, e.desc.calls)
		assert.Equal(t, "SKIPPED", e.logs.last(t)["enrichment_status"])
	})
}

func TestProjectAndLocationPath(t *testing.T) {
	e := newEnv(t)

	t.Run("WhenZoneLocationItReturnsTheSameRowsAsTheRegion", func(t *testing.T) {
		region := uuidsOf(resources(t, e.list(M{"resourceType": "volume", "projectNumber": "1111"})))
		zone := uuidsOf(resources(t, e.list(M{"resourceType": "volume", "projectNumber": "1111", "locationId": "us-east4-b"})))
		anyLocation := uuidsOf(resources(t, e.list(M{"resourceType": "volume", "projectNumber": "1111", "locationId": "-"})))
		assert.Equal(t, region, zone, "a zone does not filter rows, like Google Proxy")
		assert.Equal(t, region, anyLocation)
	})

	t.Run("WhenLocationIsAnotherRegion", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "locationId": "us-west1", "identity": M{"uuid": "vol-1"}})
		assert.Equal(t, http.StatusBadRequest, r.status, r.raw)
		r = e.list(M{"resourceType": "volume", "projectNumber": "1111", "locationId": "europe-west2-a"})
		assert.Equal(t, http.StatusBadRequest, r.status, r.raw)
	})

	t.Run("WhenProjectIsAnyGetByUUIDWorks", func(t *testing.T) {
		r := e.get(M{"resourceType": "volume", "projectNumber": "-", "identity": M{"uuid": "vol-1"}})
		assert.Equal(t, http.StatusOK, r.status, r.raw)
	})

	t.Run("WhenProjectIsNotAProjectNumber", func(t *testing.T) {
		for _, project := range []string{"my-team-prod", "--", "12a"} {
			r := e.get(M{"resourceType": "volume", "projectNumber": project, "identity": M{"uuid": "vol-1"}})
			assert.Equal(t, http.StatusBadRequest, r.status, project)
		}
	})

	t.Run("WhenProjectNumberIsSentInTheBody", func(t *testing.T) {
		r := e.post("/v1/internal/projects/1111/locations/us-east4/resources:list", `{"resourceType":"volume","projectNumber":"1111"}`)
		assert.Equal(t, http.StatusBadRequest, r.status, "the project only goes in the path")
	})

	t.Run("WhenTheOldRouteIsCalled", func(t *testing.T) {
		r := e.post("/v1/internal/resources:list", `{"resourceType":"volume"}`)
		assert.Equal(t, http.StatusNotFound, r.status)
	})

	t.Run("WhenAuditLogsTheLocation", func(t *testing.T) {
		resources(t, e.list(M{"resourceType": "volume", "projectNumber": "1111", "locationId": "us-east4-a"}))
		f := e.logs.last(t)
		assert.Equal(t, "us-east4-a", f["location"])
		assert.Equal(t, "1111", f["project_number"])
	})
}

func TestOCIReturns501(t *testing.T) {
	e := newEnv(t)
	e.oci = true
	r := e.get(M{"resourceType": "volume", "identity": M{"uuid": "vol-1"}})
	assert.Equal(t, http.StatusNotImplemented, r.status, r.raw)
	r = e.list(M{"resourceType": "volume", "projectNumber": "1111"})
	assert.Equal(t, http.StatusNotImplemented, r.status, r.raw)
	assert.Equal(t, "not_implemented", e.logs.last(t)["result"])
}

func TestAuditLog(t *testing.T) {
	e := newEnv(t)

	t.Run("list success", func(t *testing.T) {
		resources(t, e.list(M{"resourceType": "volume", "projectNumber": "1111", "includeDeleted": true, "pageSize": 10,
			"parents":     []M{{"type": "pool", "id": "pool-1"}},
			"timeFilters": []M{{"field": "updatedAt", "from": "2020-01-01T05:30:00+05:30"}},
			"filters":     M{"state": "READY"}}))
		f := e.logs.last(t)
		assert.Equal(t, "corr-test", f["correlation_id"])
		assert.Equal(t, "list", f["operation"])
		assert.Equal(t, "volume", f["resource_type"])
		assert.Equal(t, "1111", f["project_number"])
		assert.Equal(t, []string{"pool:pool-1"}, f["parent_filters"])
		assert.Equal(t, []string{"updatedAt:[2020-01-01T00:00:00Z,)"}, f["time_filters"], "bounds are normalized to UTC")
		assert.Equal(t, map[string]string{"state": "READY"}, f["field_filters"])
		assert.Equal(t, true, f["include_deleted"])
		assert.Equal(t, 10, f["page_size"])
		assert.Equal(t, 1, f["result_count"])
		assert.Equal(t, "vcp_db", f["data_source"])
		assert.Equal(t, "success", f["result"])
		assert.Equal(t, 200, f["http_status"])
		assert.Contains(t, f, "duration_ms")
	})

	t.Run("get not found", func(t *testing.T) {
		e.get(M{"resourceType": "pool", "identity": M{"uuid": "nope"}})
		f := e.logs.last(t)
		assert.Equal(t, "get", f["operation"])
		assert.Equal(t, "nope", f["resource_id"])
		assert.Equal(t, "not_found", f["result"])
		assert.Equal(t, 404, f["http_status"])
	})

	t.Run("handler validation failure", func(t *testing.T) {
		e.list(M{"resourceType": "volume"})
		f := e.logs.last(t)
		assert.Equal(t, "bad_request", f["result"])
		assert.Equal(t, 400, f["http_status"])
	})

	t.Run("schema rejection is audited too", func(t *testing.T) {
		before := len(e.logs.all())
		r := e.list(M{"resourceType": "volume", "projectNumber": "1111", "bogus": 1})
		require.Equal(t, http.StatusBadRequest, r.status)
		require.Len(t, e.logs.all(), before+1)
		f := e.logs.last(t)
		assert.Equal(t, "list", f["operation"])
		assert.Equal(t, "bad_request", f["result"])
		assert.Equal(t, "corr-test", f["correlation_id"])
	})
}

// TestNoSecretsInResponsesOrLogs reads every fixture row through every path
// and checks that the sentinel written into every secret field never leaves.
func TestNoSecretsInResponsesOrLogs(t *testing.T) {
	e := newEnv(t)
	e.desc.fn = describeWithPayloads
	var bodies []string
	for _, tc := range []struct{ resourceType, uuid string }{
		{"pool", "pool-1"}, {"volume", "vol-1"}, {"snapshot", "snap-1"}, {"replication", "rep-1"}, {"backup", "bk-1"},
		{"backupVault", "vault-1"}, {"backupPolicy", "bp-1"}, {"cmek", "kms-1"}, {"activeDirectory", "ad-1"}, {"job", "job-1"},
	} {
		r := e.get(M{"resourceType": tc.resourceType, "identity": M{"uuid": tc.uuid}})
		require.Equal(t, http.StatusOK, r.status, r.raw)
		bodies = append(bodies, r.raw)
	}
	for _, body := range []M{
		{"resourceType": "pool", "projectNumber": "1111", "includeDeleted": true},
		{"resourceType": "volume", "projectNumber": "1111", "includeDeleted": true},
		{"resourceType": "snapshot", "projectNumber": "1111", "includeDeleted": true},
		{"resourceType": "replication", "projectNumber": "1111", "includeDeleted": true},
		{"resourceType": "backup", "projectNumber": "1111", "parents": []M{{"type": "backupVault", "id": "vault-1"}}},
		{"resourceType": "backupVault", "projectNumber": "1111"},
		{"resourceType": "backupPolicy", "projectNumber": "1111"},
		{"resourceType": "cmek", "projectNumber": "1111"},
		{"resourceType": "activeDirectory", "projectNumber": "1111"},
		{"resourceType": "job", "projectNumber": "1111"},
	} {
		r := e.list(body)
		require.Equal(t, http.StatusOK, r.status, r.raw)
		bodies = append(bodies, r.raw)
	}
	for _, body := range bodies {
		assert.NotContains(t, body, sentinel)
		for _, key := range []string{`"password"`, `"secretId"`, `"kmsGrant"`, `"credentialPath"`, `"errorDetails"`, `"hmacSecretId"`, `"payload`} {
			assert.NotContains(t, strings.ToLower(body), strings.ToLower(key))
		}
	}
	assert.NotContains(t, e.logs.dump(), sentinel)
}

// TestSchemaRejectionsUseErrorSchema checks that requests rejected by the
// generated decoder answer with {code, message} and no generated type names.
func TestSchemaRejectionsUseErrorSchema(t *testing.T) {
	e := newEnv(t)
	getPath := "/v1/internal/projects/1111/locations/us-east4/resources:get"
	listPath := "/v1/internal/projects/1111/locations/us-east4/resources:list"
	sixParents := []M{}
	for i := 0; i < 6; i++ {
		sixParents = append(sixParents, M{"type": "pool", "id": "x"})
	}
	for _, tc := range []struct {
		name string
		call func() response
		want string
	}{
		{"unknown top-level field", func() response { return e.get(M{"resourceType": "volume", "identity": M{"uuid": "vol-1"}, "foo": 1}) },
			`invalid request: unexpected field "foo"`},
		{"unknown nested field", func() response { return e.get(M{"resourceType": "volume", "identity": M{"uuid": "vol-1", "id": 1}}) },
			`invalid request: field "identity": unexpected field "id"`},
		{"bad enum", func() response { return e.get(M{"resourceType": "disk", "identity": M{"uuid": "x"}}) },
			`invalid request: resourceType (invalid value: disk)`},
		{"projectNumber in body", func() response { return e.post(listPath, `{"resourceType":"volume","projectNumber":"1111"}`) },
			`invalid request: unexpected field "projectNumber"`},
		{"bad project in path", func() response {
			return e.get(M{"resourceType": "volume", "projectNumber": "my-team-prod", "identity": M{"uuid": "vol-1"}})
		},
			`invalid request: path: "projectNumber": string: no regex match: ^(-|[0-9]+)$`},
		{"bad timestamp", func() response {
			return e.list(M{"resourceType": "volume", "projectNumber": "1111", "timeFilters": []M{{"field": "createdAt", "from": "yesterday"}}})
		}, `invalid request: field "timeFilters": field "from": parsing time "yesterday" as "2006-01-02T15:04:05Z07:00": cannot parse "yesterday" as "2006"`},
		{"negative pageSize", func() response { return e.list(M{"resourceType": "volume", "projectNumber": "1111", "pageSize": -1}) },
			`invalid request: pageSize (int: value -1 less than 0)`},
		{"too many parents", func() response {
			return e.list(M{"resourceType": "volume", "projectNumber": "1111", "parents": sixParents})
		},
			`invalid request: parents (array: len 6 greater than maximum 5)`},
		{"malformed json", func() response { return e.post(getPath, `{"resourceType":`) }, `invalid request: unexpected EOF`},
		{"empty body", func() response { return e.post(getPath, ``) }, `invalid request: body required`},
		{"empty correlationId", func() response {
			return e.list(M{"resourceType": "job", "filters": M{"correlationId": ""}})
		}, `invalid request: filters (invalid: correlationId (string: len 0 less than minimum 1))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.call()
			require.Equal(t, http.StatusBadRequest, r.status, r.raw)
			assert.Equal(t, M{"code": float64(400), "message": tc.want}, M(r.body), "the API Error schema, not ogen's error_message")
			for _, internal := range []string{"error_message", "OpsGet", "OpsList", "V1", "operation ", "decode request", "callback"} {
				assert.NotContains(t, r.raw, internal)
			}
		})
	}

	t.Run("WhenRouteIsUnknown", func(t *testing.T) {
		r := e.post("/v1/internal/projects/1111/locations/us-east4/resources:nope", `{}`)
		assert.Equal(t, http.StatusNotFound, r.status)
	})
}
