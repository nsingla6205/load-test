package api

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_JobStore_CreateIfNoActive_ConcurrentRequests_CreateOneJob(t *testing.T) {
	store := NewJobStore()
	request := PrepoolSetupRequest{DeploymentName: "gcnv-test"}

	var createdCount atomic.Int32
	var waitGroup sync.WaitGroup
	for range 20 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if _, created := store.CreateIfNoActive(JobOperationSetup, request); created {
				createdCount.Add(1)
			}
		}()
	}
	waitGroup.Wait()

	assert.Equal(t, int32(1), createdCount.Load())
	assert.Len(t, store.List(), 1)
}

func Test_JobStore_List_MultipleJobs_ReturnsNewestFirst(t *testing.T) {
	store := NewJobStore()
	first, created := store.CreateIfNoActive(JobOperationSetup, PrepoolSetupRequest{DeploymentName: "gcnv-first"})
	require.True(t, created)
	store.SetDone(first.ID, nil)

	second, created := store.CreateIfNoActive(JobOperationSetup, PrepoolSetupRequest{DeploymentName: "gcnv-second"})
	require.True(t, created)

	store.mu.Lock()
	store.jobs[first.ID].CreatedAt = time.Unix(1, 0)
	store.jobs[second.ID].CreatedAt = time.Unix(2, 0)
	store.mu.Unlock()

	jobs := store.List()

	require.Len(t, jobs, 2)
	assert.Equal(t, second.ID, jobs[0].ID)
	assert.Equal(t, first.ID, jobs[1].ID)
}

func Test_JobStore_Get_ReturnsSnapshot(t *testing.T) {
	store := NewJobStore()
	job, created := store.CreateIfNoActive(JobOperationSetup, PrepoolSetupRequest{DeploymentName: "gcnv-test"})
	require.True(t, created)

	job.State = JobStateError
	stored := store.Get(job.ID)

	assert.Equal(t, JobStatePending, stored.State)
}
