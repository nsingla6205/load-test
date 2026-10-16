package opsinventory

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"go.temporal.io/api/serviceerror"
)

// fakeReader records what the handler passes to a reader.
type fakeReader struct {
	spec    Spec
	getReq  GetRequest
	listReq ListRequest
	get     func(GetRequest) (Resource, error)
	list    func(ListRequest) ([]Resource, error)
}

func (f *fakeReader) Spec() Spec { return f.spec }

func (f *fakeReader) Get(_ context.Context, req GetRequest) (Resource, error) {
	f.getReq = req
	if f.get == nil {
		return Resource{}, readstore.ErrNotFound
	}
	return f.get(req)
}

func (f *fakeReader) List(_ context.Context, req ListRequest) ([]Resource, error) {
	f.listReq = req
	if f.list == nil {
		return nil, nil
	}
	return f.list(req)
}

var project = &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-1"}, Name: "1111"}

func ownedBy(id int64) *int64 { return &id }

// fakeVolumeSpec mirrors the volume reader contract.
func fakeVolumeSpec() Spec {
	return NewVolumeReader(nil).Spec()
}

func newTestHandler(t *testing.T, readers ...Reader) (*Handler, *readstore.MockReadStore, *captureLogger) {
	t.Helper()
	store := readstore.NewMockReadStore(t)
	logs := newCaptureLogger()
	h := NewHandler(store, readers,
		WithCloudCheck(func() bool { return false }),
		WithLocationCheck(testLocationCheck),
		WithLogger(func(context.Context) log.Logger { return logs }))
	return h, store, logs
}

func getReq(rt ResourceType, uuid, name string) *oasgenserver.OpsGetResourceRequestV1 {
	req := &oasgenserver.OpsGetResourceRequestV1{ResourceType: rt}
	if uuid != "" {
		req.Identity.UUID = oasgenserver.NewOptString(uuid)
	}
	if name != "" {
		req.Identity.Name = oasgenserver.NewOptString(name)
	}
	return req
}

// gp returns get parameters for a project ("-" for any) in this test region.
func gp(project string) oasgenserver.V1GetInternalResourceParams {
	return oasgenserver.V1GetInternalResourceParams{ProjectNumber: project, LocationId: "us-east4"}
}

// lp returns list parameters for a project ("-" for any) in this test region.
func lp(project string) oasgenserver.V1ListInternalResourcesParams {
	return oasgenserver.V1ListInternalResourcesParams{ProjectNumber: project, LocationId: "us-east4"}
}

func errorOf(t *testing.T, res any) oasgenserver.Error {
	t.Helper()
	switch r := res.(type) {
	case *oasgenserver.V1GetInternalResourceBadRequest:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1GetInternalResourceNotFound:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1GetInternalResourceConflict:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1GetInternalResourceNotImplemented:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1GetInternalResourceInternalServerError:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1ListInternalResourcesBadRequest:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1ListInternalResourcesNotFound:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1ListInternalResourcesNotImplemented:
		return oasgenserver.Error(*r)
	case *oasgenserver.V1ListInternalResourcesInternalServerError:
		return oasgenserver.Error(*r)
	}
	t.Fatalf("not an error response: %T", res)
	return oasgenserver.Error{}
}

func TestNewHandler(t *testing.T) {
	t.Run("WhenReadersAreRegisteredByType", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec()}
		h := NewHandler(nil, []Reader{r})
		assert.Same(t, r, h.readers[ResourceTypeVolume])
		assert.Len(t, h.readers, 1)
		assert.NotNil(t, h.isOCI)
		assert.NotNil(t, h.logger)
	})
}

func TestHandlerGet(t *testing.T) {
	ctx := context.Background()
	volume := oasgenserver.NewOpsVolumeV1OpsResourceV1(oasgenserver.OpsVolumeV1{ResourceType: "volume", UUID: "vol-1"})

	t.Run("WhenOCI", func(t *testing.T) {
		h, _, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()})
		h.isOCI = func() bool { return true }
		res, err := h.Get(ctx, getReq(ResourceTypeVolume, "vol-1", ""), gp("-"))
		require.NoError(t, err)
		assert.Equal(t, float64(501), errorOf(t, res).Code)
	})

	t.Run("WhenNoReaderForType", func(t *testing.T) {
		h, _, _ := newTestHandler(t)
		res, _ := h.Get(ctx, getReq(ResourceTypeVolume, "vol-1", ""), gp("-"))
		assert.Equal(t, float64(400), errorOf(t, res).Code)
	})

	t.Run("WhenBodyIsNil", func(t *testing.T) {
		h, _, _ := newTestHandler(t)
		res, _ := h.Get(ctx, nil, gp("-"))
		assert.Equal(t, float64(400), errorOf(t, res).Code)
	})

	t.Run("WhenIdentityIsInvalid", func(t *testing.T) {
		h, _, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()})
		for _, req := range []*oasgenserver.OpsGetResourceRequestV1{getReq(ResourceTypeVolume, "", ""), getReq(ResourceTypeVolume, "u", "n")} {
			res, _ := h.Get(ctx, req, gp("-"))
			assert.Equal(t, "identity needs exactly one of uuid or name", errorOf(t, res).Message)
		}
	})

	t.Run("WhenByUUIDWithoutProject", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec(), get: func(GetRequest) (Resource, error) {
			return Resource{ID: 5, OwnerAccountID: ownedBy(1), Value: volume}, nil
		}}
		h, _, logs := newTestHandler(t, r)
		res, err := h.Get(ctx, getReq(ResourceTypeVolume, "vol-1", ""), oasgenserver.V1GetInternalResourceParams{ProjectNumber: "-", LocationId: "us-east4", XCorrelationID: oasgenserver.NewOptString("corr")})
		require.NoError(t, err)
		require.IsType(t, &oasgenserver.OpsResourceV1{}, res)
		assert.Equal(t, GetRequest{UUID: "vol-1"}, r.getReq)
		f := logs.last(t)
		assert.Equal(t, "corr", f["correlation_id"])
		assert.Equal(t, "success", f["result"])
		assert.Equal(t, "vcp_db", f["data_source"])
	})

	t.Run("WhenByUUIDWithParents", func(t *testing.T) {
		h, _, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()})
		req := getReq(ResourceTypeVolume, "vol-1", "")
		req.Parents = []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool, ID: "pool-1"}}
		res, _ := h.Get(ctx, req, gp("-"))
		assert.Equal(t, float64(400), errorOf(t, res).Code)
	})

	t.Run("WhenProjectDoesNotOwnTheResource", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec(), get: func(GetRequest) (Resource, error) {
			return Resource{OwnerAccountID: ownedBy(2), Value: volume}, nil
		}}
		h, store, _ := newTestHandler(t, r)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil)
		res, _ := h.Get(ctx, getReq(ResourceTypeVolume, "vol-1", ""), gp("1111"))
		assert.Equal(t, float64(404), errorOf(t, res).Code)
		assert.Same(t, project, r.getReq.Account)
	})

	t.Run("WhenProjectLookupFails", func(t *testing.T) {
		for err, code := range map[error]float64{
			readstore.ErrNotFound:      404,
			readstore.ErrAmbiguousName: 409,
			errors.New("db down"):      500,
		} {
			h, store, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()})
			store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(nil, err)
			res, _ := h.Get(ctx, getReq(ResourceTypeVolume, "vol-1", ""), gp("1111"))
			assert.Equal(t, code, errorOf(t, res).Code, err.Error())
		}
	})

	t.Run("WhenByNameItBuildsTheScope", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec(), get: func(GetRequest) (Resource, error) {
			return Resource{OwnerAccountID: ownedBy(1), Value: volume}, nil
		}}
		h, store, _ := newTestHandler(t, r)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerPool, "pool-1").Return(&readstore.Owner{ID: 22, AccountID: ownedBy(1)}, nil)
		req := getReq(ResourceTypeVolume, "", "v1")
		req.Parents = []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool, ID: "pool-1"}}
		res, err := h.Get(ctx, req, gp("1111"))
		require.NoError(t, err)
		require.IsType(t, &oasgenserver.OpsResourceV1{}, res)
		assert.Equal(t, "v1", r.getReq.Name)
		assert.Equal(t, []readstore.Eq{{Column: readstore.ColAccountID, Value: int64(1)}, {Column: readstore.ColPoolID, Value: int64(22)}}, r.getReq.NameScope)
	})

	t.Run("WhenByNameValidationFails", func(t *testing.T) {
		h, _, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()}, &fakeReader{spec: NewCmekReader(nil).Spec()})
		pool := oasgenserver.OpsParentV1{Type: oasgenserver.OpsParentTypeV1Pool, ID: "pool-1"}
		svm := oasgenserver.OpsParentV1{Type: oasgenserver.OpsParentTypeV1Svm, ID: "svm-1"}
		withParents := func(r *oasgenserver.OpsGetResourceRequestV1, parents ...oasgenserver.OpsParentV1) *oasgenserver.OpsGetResourceRequestV1 {
			r.Parents = parents
			return r
		}
		for name, tc := range map[string]struct {
			req     *oasgenserver.OpsGetResourceRequestV1
			project string
		}{
			"not supported":    {getReq(ResourceTypeCmek, "", "k"), "1111"},
			"no project":       {withParents(getReq(ResourceTypeVolume, "", "v"), pool), "-"},
			"missing parent":   {getReq(ResourceTypeVolume, "", "v"), "1111"},
			"wrong parent":     {withParents(getReq(ResourceTypeVolume, "", "v"), svm), "1111"},
			"duplicate parent": {withParents(getReq(ResourceTypeVolume, "", "v"), pool, pool), "1111"},
		} {
			res, _ := h.Get(ctx, tc.req, gp(tc.project))
			assert.Equal(t, float64(400), errorOf(t, res).Code, name)
		}
	})

	t.Run("WhenReaderFails", func(t *testing.T) {
		for err, code := range map[error]float64{
			readstore.ErrNotFound:      404,
			readstore.ErrAmbiguousName: 409,
			readstore.ErrInvalidQuery:  400,
			errors.New("db down"):      500,
		} {
			r := &fakeReader{spec: fakeVolumeSpec(), get: func(GetRequest) (Resource, error) { return Resource{}, err }}
			h, _, logs := newTestHandler(t, r)
			res, _ := h.Get(ctx, getReq(ResourceTypeVolume, "vol-1", ""), gp("-"))
			body := errorOf(t, res)
			assert.Equal(t, code, body.Code, err.Error())
			if code == 500 {
				assert.Equal(t, "internal error", body.Message, "raw errors never reach the response")
				assert.Contains(t, logs.last(t)["reason"], "db down", "but they reach the audit log")
			}
		}
	})

	t.Run("WhenJobWasEnriched", func(t *testing.T) {
		job := oasgenserver.NewOpsJobV1OpsResourceV1(oasgenserver.OpsJobV1{ResourceType: "job", Source: oasgenserver.OpsSourceV1VcpDbAndTemporal})
		r := &fakeReader{spec: NewJobReader(nil, nil).Spec(), get: func(GetRequest) (Resource, error) {
			return Resource{EnrichmentStatus: "COMPLETE", Value: job}, nil
		}}
		h, _, logs := newTestHandler(t, r)
		_, err := h.Get(ctx, getReq(ResourceTypeJob, "job-1", ""), gp("-"))
		require.NoError(t, err)
		f := logs.last(t)
		assert.Equal(t, "COMPLETE", f["enrichment_status"])
		assert.Equal(t, "vcp_db_and_temporal", f["data_source"])
	})
}

func TestResolveParents(t *testing.T) {
	ctx := context.Background()
	spec := fakeVolumeSpec()
	spec.ListParents[oasgenserver.OpsParentTypeV1Resource] = ParentSpec{Column: readstore.ColResourceUUID}

	t.Run("WhenParentHasNoOwnerTable", func(t *testing.T) {
		h, _, _ := newTestHandler(t)
		eqs, apiErr := h.resolveParents(ctx, spec, []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Resource, ID: "r"}}, nil)
		require.Nil(t, apiErr)
		assert.Equal(t, []readstore.Eq{{Column: readstore.ColResourceUUID, Value: "r"}}, eqs)
	})

	t.Run("WhenParentMatchesByUUID", func(t *testing.T) {
		h, store, _ := newTestHandler(t)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerBackupVault, "vault-1").Return(&readstore.Owner{ID: 3, AccountID: ownedBy(1)}, nil)
		eqs, apiErr := h.resolveParents(ctx, spec, []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1BackupVault, ID: "vault-1"}}, project)
		require.Nil(t, apiErr)
		assert.Equal(t, []readstore.Eq{{Column: readstore.ColVolumeBackupVaultUUID, Value: "vault-1"}}, eqs)
	})

	t.Run("WhenParentIsMissing", func(t *testing.T) {
		h, store, _ := newTestHandler(t)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerPool, "p").Return(nil, readstore.ErrNotFound)
		_, apiErr := h.resolveParents(ctx, spec, []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool, ID: "p"}}, project)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.status)
	})

	t.Run("WhenParentBelongsToAnotherProject", func(t *testing.T) {
		h, store, _ := newTestHandler(t)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerPool, "p").Return(&readstore.Owner{ID: 3, AccountID: ownedBy(2)}, nil)
		_, apiErr := h.resolveParents(ctx, spec, []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool, ID: "p"}}, project)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.status)
	})

	t.Run("WhenOwnerCheckHasNoProject", func(t *testing.T) {
		h, store, _ := newTestHandler(t)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerPool, "p").Return(&readstore.Owner{ID: 3, AccountID: ownedBy(1)}, nil)
		_, apiErr := h.resolveParents(ctx, spec, []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool, ID: "p"}}, nil)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusNotFound, apiErr.status)
	})

	t.Run("WhenLookupFails", func(t *testing.T) {
		h, store, _ := newTestHandler(t)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerPool, "p").Return(nil, errors.New("db down"))
		_, apiErr := h.resolveParents(ctx, spec, []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool, ID: "p"}}, project)
		require.NotNil(t, apiErr)
		assert.Equal(t, http.StatusInternalServerError, apiErr.status)
	})
}

func listReq(rt ResourceType) *oasgenserver.OpsListResourcesRequestV1 {
	return &oasgenserver.OpsListResourcesRequestV1{ResourceType: rt}
}

func volumeRows(ids ...int64) []Resource {
	out := make([]Resource, 0, len(ids))
	for _, id := range ids {
		out = append(out, Resource{ID: id, Value: oasgenserver.NewOpsVolumeV1OpsResourceV1(oasgenserver.OpsVolumeV1{ResourceType: "volume"})})
	}
	return out
}

func TestHandlerList(t *testing.T) {
	ctx := context.Background()
	params := lp("1111")

	t.Run("WhenOCI", func(t *testing.T) {
		h, _, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()})
		h.isOCI = func() bool { return true }
		res, _ := h.List(ctx, listReq(ResourceTypeVolume), params)
		assert.Equal(t, float64(501), errorOf(t, res).Code)
	})

	t.Run("WhenBodyIsNil", func(t *testing.T) {
		h, _, _ := newTestHandler(t)
		res, _ := h.List(ctx, nil, params)
		assert.Equal(t, float64(400), errorOf(t, res).Code)
	})

	t.Run("WhenItBuildsTheQuery", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec()}
		h, store, logs := newTestHandler(t, r)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerPool, "pool-1").Return(&readstore.Owner{ID: 22, AccountID: ownedBy(1)}, nil)
		from := time.Date(2026, 10, 1, 5, 30, 0, 0, time.FixedZone("IST", 5*3600+1800))
		req := listReq(ResourceTypeVolume)
		req.Parents = []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool, ID: "pool-1"}}
		req.TimeFilters = []oasgenserver.OpsTimeFilterV1{{Field: oasgenserver.OpsTimeFilterV1FieldCreatedAt, From: oasgenserver.NewOptDateTime(from)}}
		req.Filters = oasgenserver.NewOptOpsListFiltersV1(oasgenserver.OpsListFiltersV1{Name: oasgenserver.NewOptString("v1"), State: oasgenserver.NewOptString("READY")})
		req.IncludeDeleted = oasgenserver.NewOptBool(true)
		req.PageSize = oasgenserver.NewOptInt32(10)
		res, err := h.List(ctx, req, params)
		require.NoError(t, err)
		require.IsType(t, &oasgenserver.OpsListResourcesResponseV1{}, res)

		opts := r.listReq.Options
		assert.Equal(t, 11, opts.Limit, "one extra row detects the next page")
		assert.True(t, opts.IncludeDeleted)
		assert.Equal(t, int64(0), opts.AfterID)
		assert.ElementsMatch(t, []readstore.Eq{
			{Column: readstore.ColName, Value: "v1"},
			{Column: readstore.ColState, Value: "READY"},
			{Column: readstore.ColAccountID, Value: int64(1)},
			{Column: readstore.ColPoolID, Value: int64(22)},
		}, opts.Eq)
		require.Len(t, opts.TimeRanges, 1)
		assert.Equal(t, readstore.ColCreatedAt, opts.TimeRanges[0].Column)
		assert.Equal(t, time.UTC, opts.TimeRanges[0].From.Location())
		assert.Nil(t, opts.TimeRanges[0].To)
		assert.Same(t, project, r.listReq.Account)
		assert.Equal(t, 0, logs.last(t)["result_count"])
	})

	t.Run("WhenPageSizeIsDefaultedOrClamped", func(t *testing.T) {
		for in, want := range map[int32]int{0: 11, 1: 2, 10: 11, 50: 51, 51: 51, 5000: 51} {
			r := &fakeReader{spec: fakeVolumeSpec()}
			h, store, _ := newTestHandler(t, r)
			store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil)
			req := listReq(ResourceTypeVolume)
			req.PageSize = oasgenserver.NewOptInt32(in)
			_, _ = h.List(ctx, req, params)
			assert.Equal(t, want, r.listReq.Options.Limit, in)
		}
	})

	t.Run("WhenPageSizeIsNegative", func(t *testing.T) {
		h, _, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()})
		req := listReq(ResourceTypeVolume)
		req.PageSize = oasgenserver.NewOptInt32(-1)
		res, _ := h.List(ctx, req, params)
		assert.Equal(t, float64(400), errorOf(t, res).Code)
	})

	t.Run("WhenThereIsANextPage", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec(), list: func(ListRequest) ([]Resource, error) { return volumeRows(4, 7, 9), nil }}
		h, store, _ := newTestHandler(t, r)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil).Twice()
		req := listReq(ResourceTypeVolume)
		req.PageSize = oasgenserver.NewOptInt32(2)
		res, err := h.List(ctx, req, params)
		require.NoError(t, err)
		page := res.(*oasgenserver.OpsListResourcesResponseV1)
		assert.Len(t, page.Resources, 2)
		token := page.NextPageToken.Or("")
		require.NotEmpty(t, token)

		req.PageToken = oasgenserver.NewOptString(token)
		_, err = h.List(ctx, req, params)
		require.NoError(t, err)
		assert.Equal(t, int64(7), r.listReq.Options.AfterID, "the token holds the last row id of the page")
	})

	t.Run("WhenLastPage", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec(), list: func(ListRequest) ([]Resource, error) { return volumeRows(4), nil }}
		h, store, _ := newTestHandler(t, r)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil)
		res, _ := h.List(ctx, listReq(ResourceTypeVolume), params)
		page := res.(*oasgenserver.OpsListResourcesResponseV1)
		assert.True(t, page.NextPageToken.IsSet())
		assert.Equal(t, "", page.NextPageToken.Or("x"))
	})

	t.Run("WhenValidationFails", func(t *testing.T) {
		pool := oasgenserver.OpsParentV1{Type: oasgenserver.OpsParentTypeV1Pool, ID: "pool-1"}
		created := oasgenserver.OpsTimeFilterV1{Field: oasgenserver.OpsTimeFilterV1FieldCreatedAt, From: oasgenserver.NewOptDateTime(time.Now())}
		backupSpec := NewBackupReader(nil).Spec()
		jobSpec := NewJobReader(nil, nil).Spec()
		cases := map[string]func(*oasgenserver.OpsListResourcesRequestV1){
			"parent not allowed": func(r *oasgenserver.OpsListResourcesRequestV1) {
				r.Parents = []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Host, ID: "h"}}
			},
			"parent twice": func(r *oasgenserver.OpsListResourcesRequestV1) { r.Parents = []oasgenserver.OpsParentV1{pool, pool} },
			"parent without id": func(r *oasgenserver.OpsListResourcesRequestV1) {
				r.Parents = []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1Pool}}
			},
			"time field twice": func(r *oasgenserver.OpsListResourcesRequestV1) {
				r.TimeFilters = []oasgenserver.OpsTimeFilterV1{created, created}
			},
			"time without bounds": func(r *oasgenserver.OpsListResourcesRequestV1) {
				r.TimeFilters = []oasgenserver.OpsTimeFilterV1{{Field: created.Field}}
			},
			"time from not before to": func(r *oasgenserver.OpsListResourcesRequestV1) {
				now := time.Now()
				r.TimeFilters = []oasgenserver.OpsTimeFilterV1{{Field: created.Field, From: oasgenserver.NewOptDateTime(now), To: oasgenserver.NewOptDateTime(now)}}
			},
			"unknown state": func(r *oasgenserver.OpsListResourcesRequestV1) {
				r.Filters = oasgenserver.NewOptOpsListFiltersV1(oasgenserver.OpsListFiltersV1{State: oasgenserver.NewOptString("HAPPY")})
			},
			"job-only filter": func(r *oasgenserver.OpsListResourcesRequestV1) {
				r.Filters = oasgenserver.NewOptOpsListFiltersV1(oasgenserver.OpsListFiltersV1{CorrelationId: oasgenserver.NewOptString("c")})
			},
			"bad page token": func(r *oasgenserver.OpsListResourcesRequestV1) { r.PageToken = oasgenserver.NewOptString("!!") },
		}
		for name, change := range cases {
			h, _, logs := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()}, &fakeReader{spec: backupSpec}, &fakeReader{spec: jobSpec})
			req := listReq(ResourceTypeVolume)
			change(req)
			res, _ := h.List(ctx, req, params)
			assert.Equal(t, float64(400), errorOf(t, res).Code, name)
			assert.Equal(t, "bad_request", logs.last(t)["result"], name)
		}

		h, _, _ := newTestHandler(t, &fakeReader{spec: backupSpec}, &fakeReader{spec: jobSpec}, &fakeReader{spec: NewCmekReader(nil).Spec()})
		res, _ := h.List(ctx, listReq(ResourceTypeBackup), params)
		assert.Contains(t, errorOf(t, res).Message, "needs parent backupVault")

		res, _ = h.List(ctx, listReq(ResourceTypeJob), lp("-"))
		assert.Contains(t, errorOf(t, res).Message, "needs another filter")

		res, _ = h.List(ctx, listReq(ResourceTypeCmek), lp("-"))
		assert.Contains(t, errorOf(t, res).Message, "a project number is required")

		cmekReq := listReq(ResourceTypeCmek)
		cmekReq.Filters = oasgenserver.NewOptOpsListFiltersV1(oasgenserver.OpsListFiltersV1{Name: oasgenserver.NewOptString("x")})
		res, _ = h.List(ctx, cmekReq, params)
		assert.Contains(t, errorOf(t, res).Message, "filter name is not supported")
	})

	t.Run("WhenJobListHasNoProject", func(t *testing.T) {
		r := &fakeReader{spec: NewJobReader(nil, nil).Spec()}
		h, _, logs := newTestHandler(t, r)
		req := &oasgenserver.OpsListResourcesRequestV1{ResourceType: ResourceTypeJob,
			Filters: oasgenserver.NewOptOpsListFiltersV1(oasgenserver.OpsListFiltersV1{CorrelationId: oasgenserver.NewOptString("corr")})}
		res, err := h.List(ctx, req, lp("-"))
		require.NoError(t, err)
		require.IsType(t, &oasgenserver.OpsListResourcesResponseV1{}, res)
		assert.Nil(t, r.listReq.Account)
		assert.Equal(t, []readstore.Eq{{Column: readstore.ColCorrelationID, Value: "corr"}}, r.listReq.Options.Eq, "no account predicate")
		assert.Equal(t, "SKIPPED", logs.last(t)["enrichment_status"])
	})

	t.Run("WhenBackupListHasNoAccountColumn", func(t *testing.T) {
		r := &fakeReader{spec: NewBackupReader(nil).Spec()}
		h, store, _ := newTestHandler(t, r)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil)
		store.EXPECT().LookupOwner(mock.Anything, readstore.OwnerBackupVault, "vault-1").Return(&readstore.Owner{ID: 3, AccountID: ownedBy(1)}, nil)
		req := listReq(ResourceTypeBackup)
		req.Parents = []oasgenserver.OpsParentV1{{Type: oasgenserver.OpsParentTypeV1BackupVault, ID: "vault-1"}}
		_, err := h.List(ctx, req, params)
		require.NoError(t, err)
		assert.Equal(t, []readstore.Eq{{Column: readstore.ColBackupVaultID, Value: int64(3)}}, r.listReq.Options.Eq)
	})

	t.Run("WhenReaderFails", func(t *testing.T) {
		r := &fakeReader{spec: fakeVolumeSpec(), list: func(ListRequest) ([]Resource, error) { return nil, errors.New("db down") }}
		h, store, _ := newTestHandler(t, r)
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(project, nil)
		res, _ := h.List(ctx, listReq(ResourceTypeVolume), params)
		assert.Equal(t, float64(500), errorOf(t, res).Code)
	})

	t.Run("WhenProjectIsUnknown", func(t *testing.T) {
		h, store, _ := newTestHandler(t, &fakeReader{spec: fakeVolumeSpec()})
		store.EXPECT().GetAccountByName(mock.Anything, "1111").Return(nil, readstore.ErrNotFound)
		res, _ := h.List(ctx, listReq(ResourceTypeVolume), params)
		assert.Equal(t, "project not found", errorOf(t, res).Message)
	})
}

func TestFromStoreError(t *testing.T) {
	assert.Equal(t, http.StatusNotFound, fromStoreError(readstore.ErrNotFound, "volume").status)
	assert.Equal(t, http.StatusConflict, fromStoreError(readstore.ErrAmbiguousName, "volume").status)
	assert.Equal(t, http.StatusBadRequest, fromStoreError(readstore.ErrInvalidQuery, "volume").status)
	passed := badRequest("x")
	assert.Same(t, passed, fromStoreError(passed, "volume"), "apiErrors pass through")
	internal := fromStoreError(errors.New("connection reset"), "volume")
	assert.Equal(t, http.StatusInternalServerError, internal.status)
	assert.Equal(t, "internal error", internal.message)
	assert.Contains(t, internal.Error(), "connection reset")
}

func TestErrorResponses(t *testing.T) {
	for status, want := range map[int]any{
		400: &oasgenserver.V1GetInternalResourceBadRequest{}, 404: &oasgenserver.V1GetInternalResourceNotFound{},
		409: &oasgenserver.V1GetInternalResourceConflict{}, 501: &oasgenserver.V1GetInternalResourceNotImplemented{},
		500: &oasgenserver.V1GetInternalResourceInternalServerError{}, 418: &oasgenserver.V1GetInternalResourceInternalServerError{},
	} {
		assert.IsType(t, want, getErrorResponse(&apiError{status: status}), status)
	}
	for status, want := range map[int]any{
		400: &oasgenserver.V1ListInternalResourcesBadRequest{}, 404: &oasgenserver.V1ListInternalResourcesNotFound{},
		501: &oasgenserver.V1ListInternalResourcesNotImplemented{}, 500: &oasgenserver.V1ListInternalResourcesInternalServerError{},
	} {
		assert.IsType(t, want, listErrorResponse(&apiError{status: status}), status)
	}
}

func TestAuditResult(t *testing.T) {
	for status, want := range map[int]string{200: "success", 400: "bad_request", 404: "not_found", 409: "conflict", 501: "not_implemented", 500: "error", 503: "error"} {
		assert.Equal(t, want, auditResult(status), status)
	}
}

func TestErrorHandler(t *testing.T) {
	newRequest := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/internal/projects/1111/locations/us-east4/resources:list", nil)
		r.Header.Set("x-correlation-id", "corr-1")
		return r
	}
	decodeErr := func(opID string, err error) error {
		return &ogenerrors.DecodeRequestError{OperationContext: ogenerrors.OperationContext{Name: "V1ListInternalResources", ID: opID}, Err: err}
	}

	t.Run("WhenOurOperationIsRejected", func(t *testing.T) {
		h, _, logs := newTestHandler(t)
		called := false
		next := func(context.Context, http.ResponseWriter, *http.Request, error) { called = true }
		rec := httptest.NewRecorder()
		h.ErrorHandler(next)(context.Background(), rec, newRequest(), decodeErr(listOperationID, errors.New(`decode application/json: decode OpsListResourcesRequestV1: callback: unexpected field "foo"`)))
		assert.False(t, called, "ops inventory errors are written here, not by ogen's handler")
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"code":400,"message":"invalid request: unexpected field \"foo\""}`, rec.Body.String())
		f := logs.last(t)
		assert.Equal(t, "list", f["operation"])
		assert.Equal(t, "corr-1", f["correlation_id"])
		assert.Equal(t, 400, f["http_status"])
		assert.Equal(t, "request rejected by schema validation", f["reason"], "the decoder message is not logged")
	})

	t.Run("WhenOurOperationFailsWithAServerError", func(t *testing.T) {
		h, _, _ := newTestHandler(t)
		rec := httptest.NewRecorder()
		err := &ogenerrors.DecodeRequestError{OperationContext: ogenerrors.OperationContext{ID: getOperationID}, Err: errors.New("boom")}
		// A plain error wrapped with an op context but no decode status maps to 500 in ogen.
		h.ErrorHandler(nil)(context.Background(), rec, newRequest(), struct {
			ogenerrors.OperationContext
			error
		}{err.OperationContext, errors.New("encode response: boom")})
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.JSONEq(t, `{"code":500,"message":"internal error"}`, rec.Body.String(), "server-side detail is not returned")
	})

	t.Run("WhenAnotherOperationIsRejected", func(t *testing.T) {
		h, _, logs := newTestHandler(t)
		called := false
		h.ErrorHandler(func(context.Context, http.ResponseWriter, *http.Request, error) { called = true })(
			context.Background(), httptest.NewRecorder(), newRequest(), decodeErr("v1_createPool", errors.New("x")))
		assert.True(t, called, "other Core routes keep ogen's default handler")
		assert.Empty(t, logs.all())
	})

	t.Run("WhenErrorHasNoOperation", func(t *testing.T) {
		h, _, logs := newTestHandler(t)
		called := false
		h.ErrorHandler(func(context.Context, http.ResponseWriter, *http.Request, error) { called = true })(
			context.Background(), httptest.NewRecorder(), newRequest(), errors.New("x"))
		assert.True(t, called)
		assert.Empty(t, logs.all())
	})
}

func TestRejectionMessage(t *testing.T) {
	for in, want := range map[string]string{
		`operation V1GetInternalResource: decode request: decode application/json: decode OpsGetResourceRequestV1: callback: unexpected field "foo"`:                                                         `invalid request: unexpected field "foo"`,
		`operation V1GetInternalResource: decode request: decode application/json: decode OpsGetResourceRequestV1: callback: decode field "identity": decode OpsIdentityV1: callback: unexpected field "id"`: `invalid request: field "identity": unexpected field "id"`,
		`operation V1GetInternalResource: decode request: validate: invalid: resourceType (invalid value: disk)`:                                                                                             `invalid request: resourceType (invalid value: disk)`,
		`operation V1ListInternalResources: decode request: decode application/json: decode OpsListResourcesRequestV1: callback: decode field "filters": decode OptOpsListFiltersV1: x`:                      `invalid request: field "filters": x`,
		`operation V1GetInternalResource: decode request`: `invalid request`,
	} {
		assert.Equal(t, want, rejectionMessage(errors.New(in)), in)
	}
}

func TestUnavailableReason(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "temporal describe timed out", unavailableReason(ctx, context.DeadlineExceeded))
	assert.Equal(t, "temporal describe timed out", unavailableReason(ctx, serviceerror.NewDeadlineExceeded("context deadline exceeded")), "gRPC deadline from the Temporal client")
	assert.Equal(t, "temporal describe failed: Unavailable", unavailableReason(ctx, serviceerror.NewUnavailable("down")))
}
