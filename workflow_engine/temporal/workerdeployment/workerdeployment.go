// Package workerdeployment wraps Temporal's Experimental Worker Deployment APIs
// for VLM D-patch promote / drain / retire flows (SDK v1.42 BuildID form).
package workerdeployment

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.temporal.io/sdk/client"
)

// DefaultDeploymentName is used when neither an explicit name nor a task queue is provided.
const DefaultDeploymentName = "vsa-lifecycle-manager"

// DefaultPollerFreshness is the maximum age of a Temporal poll accepted as evidence
// that a worker is actively polling.
const DefaultPollerFreshness = 2 * time.Minute

// Client is the Temporal management surface used by Worker Deployment helpers.
type Client interface {
	GetHandle(name string) client.WorkerDeploymentHandle
	DescribeTaskQueuePollers(ctx context.Context, taskQueue, deploymentName, buildID string) ([]PollerInfo, error)
}

// PollerInfo identifies a Worker Deployment poller observed by Temporal.
type PollerInfo struct {
	DeploymentName string
	BuildID        string
	LastAccessTime time.Time
}

type temporalClient struct {
	client client.Client
}

func (c *temporalClient) GetHandle(name string) client.WorkerDeploymentHandle {
	return c.client.WorkerDeploymentClient().GetHandle(name)
}

func (c *temporalClient) DescribeTaskQueuePollers(
	ctx context.Context,
	taskQueue string,
	deploymentName string,
	buildID string,
) ([]PollerInfo, error) {
	description, err := c.client.DescribeTaskQueueEnhanced(ctx, client.DescribeTaskQueueEnhancedOptions{
		TaskQueue: taskQueue,
		Versions: &client.TaskQueueVersionSelection{
			BuildIDs: []string{VersionID(deploymentName, buildID)},
		},
		TaskQueueTypes: []client.TaskQueueType{
			client.TaskQueueTypeWorkflow,
			client.TaskQueueTypeActivity,
		},
		ReportPollers: true,
	})
	if err != nil {
		return nil, err
	}

	return pollersFromTaskQueueDescription(description), nil
}

// FromTemporal adapts a Temporal client.Client.
func FromTemporal(c client.Client) Client {
	return &temporalClient{client: c}
}

// ResolveDeploymentName returns the Worker Deployment name.
// Explicit overrides win. Otherwise the task queue is used with '.' replaced by '-'
// because Temporal forbids '.' in Worker Deployment names.
func ResolveDeploymentName(explicit, taskQueue string) string {
	if name := strings.TrimSpace(explicit); name != "" {
		return sanitizeDeploymentName(name)
	}
	if q := strings.TrimSpace(taskQueue); q != "" {
		return sanitizeDeploymentName(q)
	}
	return DefaultDeploymentName
}

func sanitizeDeploymentName(name string) string {
	return strings.ReplaceAll(strings.TrimSpace(name), ".", "-")
}

// VersionID builds the Temporal Worker Deployment version string
// "<deployment_name>:<build_id>" used by DescribeTaskQueueEnhanced.
func VersionID(deploymentName, buildID string) string {
	return fmt.Sprintf("%s:%s", deploymentName, buildID)
}

// BuildIDFromVersion extracts the build ID from "<deployment_name>:<build_id>".
func BuildIDFromVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "__unversioned__" {
		return ""
	}
	if i := strings.Index(version, ":"); i >= 0 && i+1 < len(version) {
		return version[i+1:]
	}
	return version
}

func versionBuildID(v client.WorkerDeploymentVersionSummary) string {
	return strings.TrimSpace(v.Version.BuildID)
}

func currentVersionBuildID(cfg client.WorkerDeploymentRoutingConfig) string {
	if cfg.CurrentVersion == nil {
		return ""
	}
	return strings.TrimSpace(cfg.CurrentVersion.BuildID)
}

// PromoteBuild sets buildID as the Current Version of the deployment.
func PromoteBuild(ctx context.Context, wdc Client, deploymentName, buildID string) error {
	if err := requireNames(deploymentName, buildID); err != nil {
		return err
	}
	_, err := wdc.GetHandle(deploymentName).SetCurrentVersion(ctx, client.WorkerDeploymentSetCurrentVersionOptions{
		BuildID: buildID,
	})
	if err != nil {
		return fmt.Errorf("promote build %q on deployment %q: %w", buildID, deploymentName, err)
	}
	return nil
}

// CurrentBuildID returns the Current Version build ID, or "" if unversioned / unset.
func CurrentBuildID(ctx context.Context, wdc Client, deploymentName string) (string, error) {
	if strings.TrimSpace(deploymentName) == "" {
		return "", fmt.Errorf("deployment name is required")
	}
	desc, err := wdc.GetHandle(deploymentName).Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	if err != nil {
		return "", fmt.Errorf("describe deployment %q: %w", deploymentName, err)
	}
	return currentVersionBuildID(desc.Info.RoutingConfig), nil
}

// IsBuildPolling reports whether Temporal has a recent poller for the task queue,
// Worker Deployment, and build ID.
func IsBuildPolling(
	ctx context.Context,
	temporalClient Client,
	taskQueue string,
	deploymentName string,
	buildID string,
	maxPollerAge time.Duration,
) (bool, error) {
	if err := requireNames(deploymentName, buildID); err != nil {
		return false, err
	}
	if strings.TrimSpace(taskQueue) == "" {
		return false, fmt.Errorf("task queue is required")
	}
	if maxPollerAge <= 0 {
		maxPollerAge = DefaultPollerFreshness
	}
	pollers, err := temporalClient.DescribeTaskQueuePollers(ctx, taskQueue, deploymentName, buildID)
	if err != nil {
		return false, fmt.Errorf("describe task queue %q for build %q: %w", taskQueue, buildID, err)
	}

	now := time.Now()
	for _, poller := range pollers {
		if poller.DeploymentName != deploymentName ||
			poller.BuildID != buildID ||
			poller.LastAccessTime.IsZero() {
			continue
		}
		if now.Sub(poller.LastAccessTime) <= maxPollerAge {
			return true, nil
		}
	}
	return false, nil
}

// LatestPolledBuildID returns the most recently created polled build, or "".
func LatestPolledBuildID(ctx context.Context, wdc Client, deploymentName string) (string, error) {
	if strings.TrimSpace(deploymentName) == "" {
		return "", fmt.Errorf("deployment name is required")
	}
	desc, err := wdc.GetHandle(deploymentName).Describe(ctx, client.WorkerDeploymentDescribeOptions{})
	if err != nil {
		return "", fmt.Errorf("describe deployment %q: %w", deploymentName, err)
	}
	s := desc.Info.VersionSummaries
	if len(s) == 0 {
		return "", nil
	}
	latest := s[0]
	for i := 1; i < len(s); i++ {
		if s[i].CreateTime.After(latest.CreateTime) {
			latest = s[i]
		}
	}
	return versionBuildID(latest), nil
}

// IsBuildDrained reports whether buildID is fully drained.
func IsBuildDrained(ctx context.Context, wdc Client, deploymentName, buildID string) (bool, error) {
	if err := requireNames(deploymentName, buildID); err != nil {
		return false, err
	}
	desc, err := wdc.GetHandle(deploymentName).DescribeVersion(ctx, client.WorkerDeploymentDescribeVersionOptions{
		BuildID: buildID,
	})
	if err != nil {
		return false, fmt.Errorf("describe version %q on deployment %q: %w", buildID, deploymentName, err)
	}
	di := desc.Info.DrainageInfo
	return di != nil && di.DrainageStatus == client.WorkerDeploymentVersionDrainageStatusDrained, nil
}

// DeleteBuild removes a drained version record (pods must already be stopped).
func DeleteBuild(ctx context.Context, wdc Client, deploymentName, buildID string) error {
	if err := requireNames(deploymentName, buildID); err != nil {
		return err
	}
	_, err := wdc.GetHandle(deploymentName).DeleteVersion(ctx, client.WorkerDeploymentDeleteVersionOptions{
		BuildID: buildID,
	})
	if err != nil {
		return fmt.Errorf("delete version %q on deployment %q: %w", buildID, deploymentName, err)
	}
	return nil
}

// WaitForPolling blocks until buildID is polling or ctx ends.
func WaitForPolling(
	ctx context.Context,
	temporalClient Client,
	taskQueue string,
	deploymentName string,
	buildID string,
	pollEvery time.Duration,
) error {
	if err := requireNames(deploymentName, buildID); err != nil {
		return err
	}
	if strings.TrimSpace(taskQueue) == "" {
		return fmt.Errorf("task queue is required")
	}
	if pollEvery <= 0 {
		pollEvery = 5 * time.Second
	}
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		ok, err := IsBuildPolling(
			ctx,
			temporalClient,
			taskQueue,
			deploymentName,
			buildID,
			DefaultPollerFreshness,
		)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// WaitForDrain blocks until buildID is drained or ctx ends.
func WaitForDrain(ctx context.Context, wdc Client, deploymentName, buildID string, pollEvery time.Duration) error {
	if err := requireNames(deploymentName, buildID); err != nil {
		return err
	}
	if pollEvery <= 0 {
		pollEvery = 15 * time.Second
	}
	handle := wdc.GetHandle(deploymentName)
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		desc, err := handle.DescribeVersion(ctx, client.WorkerDeploymentDescribeVersionOptions{
			BuildID: buildID,
		})
		if err != nil {
			return fmt.Errorf("describe version %q on deployment %q: %w", buildID, deploymentName, err)
		}
		if di := desc.Info.DrainageInfo; di != nil &&
			di.DrainageStatus == client.WorkerDeploymentVersionDrainageStatusDrained {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func pollersFromTaskQueueDescription(desc client.TaskQueueDescription) []PollerInfo {
	// SDK v1.42 still reports pollers on the deprecated VersionsInfo field.
	// VersioningInfo only has current/ramping routing, so read pollers by field
	// name instead of the deprecated type/field identifiers that staticcheck flags.
	versions := reflect.ValueOf(desc).FieldByName("VersionsInfo")
	if !versions.IsValid() || versions.Kind() != reflect.Map {
		return nil
	}
	var pollers []PollerInfo
	for _, versionKey := range versions.MapKeys() {
		typesInfo := versions.MapIndex(versionKey).FieldByName("TypesInfo")
		if !typesInfo.IsValid() || typesInfo.Kind() != reflect.Map {
			continue
		}
		for _, typeKey := range typesInfo.MapKeys() {
			pollerList := typesInfo.MapIndex(typeKey).FieldByName("Pollers")
			if !pollerList.IsValid() || pollerList.Kind() != reflect.Slice {
				continue
			}
			for i := 0; i < pollerList.Len(); i++ {
				poller := pollerList.Index(i)
				options := poller.FieldByName("WorkerDeploymentPollerOptions")
				if !options.IsValid() || options.Kind() != reflect.Pointer || options.IsNil() {
					continue
				}
				opt := options.Elem()
				lastAccess, _ := poller.FieldByName("LastAccessTime").Interface().(time.Time)
				pollers = append(pollers, PollerInfo{
					DeploymentName: opt.FieldByName("DeploymentName").String(),
					BuildID:        opt.FieldByName("BuildID").String(),
					LastAccessTime: lastAccess,
				})
			}
		}
	}
	return pollers
}

func requireNames(deploymentName, buildID string) error {
	if strings.TrimSpace(deploymentName) == "" {
		return fmt.Errorf("deployment name is required")
	}
	if strings.TrimSpace(buildID) == "" {
		return fmt.Errorf("build ID is required")
	}
	return nil
}
