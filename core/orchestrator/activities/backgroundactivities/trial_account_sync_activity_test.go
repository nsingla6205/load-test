package backgroundactivities

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/trial"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
)

func withLocalRegion(t *testing.T, region string) {
	t.Helper()
	orig := env.Region
	env.Region = region
	t.Cleanup(func() { env.Region = orig })
}

func expectTrialSyncListAccounts(t *testing.T, mockSE *database.MockStorage, ctx context.Context, accounts []*datamodel.Account, listErr error) {
	t.Helper()
	mockSE.EXPECT().GetAccountsWithFilter(ctx, mock.Anything, mock.Anything).Return(accounts, listErr).Once()
}

func expectTrialSyncListAccountsPaginated(
	t *testing.T,
	mockSE *database.MockStorage,
	ctx context.Context,
	pageSize int,
	pages [][]*datamodel.Account,
) {
	t.Helper()
	for i, page := range pages {
		offset := i * pageSize
		mockSE.EXPECT().
			GetAccountsWithFilter(ctx, mock.Anything, mock.MatchedBy(func(p *dbutils.Pagination) bool {
				return p != nil && p.Offset == offset && p.Limit == pageSize
			})).
			Return(page, nil).
			Once()
	}
}

func testTrialClient(t *testing.T, handler http.HandlerFunc) *trial.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return trial.NewClient(
		func(context.Context) (string, error) { return "tok", nil },
		trial.WithBaseURL(server.URL),
		trial.WithHTTPClient(server.Client()),
	)
}

func TestReconcileTrials(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	account := &datamodel.Account{Name: "proj-1"}
	resourceName := "projects/proj-1/locations/us-central1/trial"

	t.Run("empty resource names", func(t *testing.T) {
		outcomes := reconcileTrials(ctx, nil, []trialReconcileRequest{{Account: account}})
		require.Len(t, outcomes, 1)
		assert.ErrorIs(t, outcomes[0].Err, trial.ErrTrialNotFound)
	})

	t.Run("returns first successful trial", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(r.URL.Path, "europe-west1") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":      resourceName,
				"startTime": start.Format(time.RFC3339),
				"endTime":   end.Format(time.RFC3339),
			})
		}))
		defer server.Close()

		client := trial.NewClient(
			func(context.Context) (string, error) { return "token", nil },
			trial.WithBaseURL(server.URL),
			trial.WithHTTPClient(server.Client()),
		)

		outcomes := reconcileTrials(ctx, client, []trialReconcileRequest{{
			Account:       account,
			ResourceNames: []string{"projects/proj-1/locations/europe-west1/trial", resourceName},
		}})
		require.Len(t, outcomes, 1)
		require.NoError(t, outcomes[0].Err)
		require.NotNil(t, outcomes[0].Trial)
		assert.Equal(t, resourceName, outcomes[0].Trial.Name)
	})

	t.Run("propagates lookup error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		client := trial.NewClient(
			func(context.Context) (string, error) { return "token", nil },
			trial.WithBaseURL(server.URL),
			trial.WithHTTPClient(server.Client()),
		)

		outcomes := reconcileTrials(ctx, client, []trialReconcileRequest{{
			Account:       account,
			ResourceNames: []string{resourceName},
		}})
		require.Len(t, outcomes, 1)
		assert.Error(t, outcomes[0].Err)
		assert.False(t, errors.Is(outcomes[0].Err, trial.ErrTrialNotFound))
	})

	t.Run("not found when all resources missing", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		client := trial.NewClient(
			func(context.Context) (string, error) { return "token", nil },
			trial.WithBaseURL(server.URL),
			trial.WithHTTPClient(server.Client()),
		)

		outcomes := reconcileTrials(ctx, client, []trialReconcileRequest{{
			Account:       account,
			ResourceNames: []string{resourceName},
		}})
		require.Len(t, outcomes, 1)
		assert.ErrorIs(t, outcomes[0].Err, trial.ErrTrialNotFound)
	})
}

func TestTrialAccountSyncActivity_SyncTrialAccounts(t *testing.T) {
	ctx := context.Background()
	const testRegion = "us-central1"

	start := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	exit := "ENDED"
	resourceName := "projects/proj-1/locations/us-central1/trial"

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "acct-uuid", ID: 1},
		Name:      "proj-1",
		State:     datamodel.AccountStateEnabled,
		AccountMetadata: &datamodel.AccountMetadata{
			TrialMode: &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end},
		},
	}

	t.Run("empty local region skips reconcile without error", func(t *testing.T) {
		withLocalRegion(t, "")

		mockSE := database.NewMockStorage(t)
		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{account}, nil)

		err := (&TrialAccountSyncActivity{
			SE:        mockSE,
			CCFE:      testTrialClient(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected HTTP call") }),
			BatchSize: 10,
		}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("no eligible accounts", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		metrics.RegisterTrialAccountSyncRunCounter()
		defer prometheus.Unregister(metrics.TrialAccountSyncRunCounter)

		startedBefore := testutil.ToFloat64(metrics.TrialAccountSyncRunCounter.WithLabelValues("started"))
		succeededBefore := testutil.ToFloat64(metrics.TrialAccountSyncRunCounter.WithLabelValues("succeeded"))

		mockSE := database.NewMockStorage(t)
		expectTrialSyncListAccounts(t, mockSE, ctx, nil, nil)

		err := (&TrialAccountSyncActivity{SE: mockSE}).SyncTrialAccounts(ctx)
		require.NoError(t, err)

		assert.Equal(t, startedBefore+1, testutil.ToFloat64(metrics.TrialAccountSyncRunCounter.WithLabelValues("started")))
		assert.Equal(t, succeededBefore+1, testutil.ToFloat64(metrics.TrialAccountSyncRunCounter.WithLabelValues("succeeded")))
	})

	t.Run("processes accounts in configured batches", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		mockSE := database.NewMockStorage(t)

		account2 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acct-uuid-2", ID: 2},
			Name:      "proj-2",
			State:     datamodel.AccountStateEnabled,
			AccountMetadata: &datamodel.AccountMetadata{
				TrialMode: &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end},
			},
		}
		account3 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acct-uuid-3", ID: 3},
			Name:      "proj-3",
			State:     datamodel.AccountStateEnabled,
			AccountMetadata: &datamodel.AccountMetadata{
				TrialMode: &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end},
			},
		}

		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{account, account2, account3}, nil)

		client := testTrialClient(t, func(w http.ResponseWriter, r *http.Request) {
			name := "projects/proj-3/locations/us-central1/trial"
			switch r.URL.Path {
			case "/v1internal/projects/proj-1/locations/us-central1/trial":
				name = resourceName
			case "/v1internal/projects/proj-2/locations/us-central1/trial":
				name = "projects/proj-2/locations/us-central1/trial"
			}
			trial := datamodel.InternalTrial{Name: name, StartTime: start, EndTime: end}
			if name == resourceName {
				reason := datamodel.TrialExitReason(exit)
				trial.ExitReason = &reason
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(trial)
		})

		mockSE.EXPECT().UpdateAccountTrialMetadata(ctx, account, mock.Anything).Return(nil)
		mockSE.EXPECT().UpdateAccountTrialMetadata(ctx, account2, mock.Anything).Return(errors.New("db write failed"))
		mockSE.EXPECT().UpdateAccountTrialMetadata(ctx, account3, mock.Anything).Return(nil)

		err := (&TrialAccountSyncActivity{SE: mockSE, CCFE: client, BatchSize: 2}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("lookup failure is logged and skipped", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		mockSE := database.NewMockStorage(t)
		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{account}, nil)

		client := testTrialClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		err := (&TrialAccountSyncActivity{SE: mockSE, CCFE: client, BatchSize: 10}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("not found skips account", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		mockSE := database.NewMockStorage(t)
		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{account}, nil)

		client := testTrialClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})

		err := (&TrialAccountSyncActivity{SE: mockSE, CCFE: client, BatchSize: 10}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("list accounts error is returned", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		mockSE := database.NewMockStorage(t)
		listErr := errors.New("database unavailable")
		expectTrialSyncListAccounts(t, mockSE, ctx, nil, listErr)

		err := (&TrialAccountSyncActivity{SE: mockSE}).SyncTrialAccounts(ctx)
		require.ErrorIs(t, err, listErr)
	})

	t.Run("empty account name is skipped", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		mockSE := database.NewMockStorage(t)
		blankNameAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "blank-name"},
			Name:      "   ",
			State:     datamodel.AccountStateEnabled,
			AccountMetadata: &datamodel.AccountMetadata{
				TrialMode: &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end},
			},
		}
		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{blankNameAccount}, nil)

		err := (&TrialAccountSyncActivity{
			SE:        mockSE,
			CCFE:      testTrialClient(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("unexpected HTTP call") }),
			BatchSize: 10,
		}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("persists exit reason from producer", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		mockSE := database.NewMockStorage(t)
		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{account}, nil)

		client := testTrialClient(t, func(w http.ResponseWriter, r *http.Request) {
			reason := datamodel.TrialExitReason(exit)
			_ = json.NewEncoder(w).Encode(datamodel.InternalTrial{
				Name:       resourceName,
				StartTime:  start,
				EndTime:    end,
				ExitReason: &reason,
			})
		})

		mockSE.EXPECT().UpdateAccountTrialMetadata(ctx, account, mock.MatchedBy(func(mode *datamodel.AccountTrialMode) bool {
			return mode != nil &&
				mode.ExitReason != nil &&
				*mode.ExitReason == exit &&
				mode.StartTime != nil &&
				start.Equal(*mode.StartTime) &&
				mode.EndTime != nil &&
				end.Equal(*mode.EndTime)
		})).Return(nil).Once()

		err := (&TrialAccountSyncActivity{SE: mockSE, CCFE: client, BatchSize: 10}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("all accounts not found does not persist", func(t *testing.T) {
		withLocalRegion(t, testRegion)

		mockSE := database.NewMockStorage(t)
		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{account}, nil)

		client := testTrialClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})

		err := (&TrialAccountSyncActivity{SE: mockSE, CCFE: client, BatchSize: 10}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("paginates list accounts", func(t *testing.T) {
		withLocalRegion(t, testRegion)
		require.NoError(t, os.Setenv("TRIAL_ACCOUNT_SYNC_ACCOUNT_PAGE_SIZE", "1"))
		t.Cleanup(func() { _ = os.Unsetenv("TRIAL_ACCOUNT_SYNC_ACCOUNT_PAGE_SIZE") })

		account2 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acct-uuid-page-2", ID: 2},
			Name:      "proj-page-2",
			State:     datamodel.AccountStateEnabled,
			AccountMetadata: &datamodel.AccountMetadata{
				TrialMode: &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end},
			},
		}

		mockSE := database.NewMockStorage(t)
		// Activity keeps paging while a full page is returned; an empty page ends the loop.
		expectTrialSyncListAccountsPaginated(t, mockSE, ctx, 1, [][]*datamodel.Account{
			{account},
			{account2},
			nil,
		})

		client := testTrialClient(t, func(w http.ResponseWriter, r *http.Request) {
			name := "projects/proj-page-2/locations/us-central1/trial"
			if strings.Contains(r.URL.Path, "proj-1") {
				name = resourceName
			}
			_ = json.NewEncoder(w).Encode(datamodel.InternalTrial{Name: name, StartTime: start, EndTime: end})
		})

		mockSE.EXPECT().UpdateAccountTrialMetadata(ctx, account, mock.Anything).Return(nil)
		mockSE.EXPECT().UpdateAccountTrialMetadata(ctx, account2, mock.Anything).Return(nil)

		err := (&TrialAccountSyncActivity{SE: mockSE, CCFE: client, BatchSize: 10}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})

	t.Run("uses LOCAL_REGION env for trial resource path", func(t *testing.T) {
		withLocalRegion(t, "europe-west1")

		mockSE := database.NewMockStorage(t)
		euAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "eu-acct"},
			Name:      "eu-proj",
			State:     datamodel.AccountStateEnabled,
			AccountMetadata: &datamodel.AccountMetadata{
				TrialMode: &datamodel.AccountTrialMode{StartTime: &start, EndTime: &end},
			},
		}
		expectTrialSyncListAccounts(t, mockSE, ctx, []*datamodel.Account{euAccount}, nil)

		euResource := "projects/eu-proj/locations/europe-west1/trial"
		client := testTrialClient(t, func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/v1internal/projects/eu-proj/locations/europe-west1/trial", r.URL.Path)
			_ = json.NewEncoder(w).Encode(datamodel.InternalTrial{Name: euResource, StartTime: start, EndTime: end})
		})
		mockSE.EXPECT().UpdateAccountTrialMetadata(ctx, euAccount, mock.Anything).Return(nil)

		err := (&TrialAccountSyncActivity{SE: mockSE, CCFE: client, BatchSize: 10}).SyncTrialAccounts(ctx)
		require.NoError(t, err)
	})
}
