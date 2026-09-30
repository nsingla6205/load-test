package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/names"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/prepoolinfra"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

const maxRequestBodyBytes = 1 << 20

var (
	projectNumberPattern   = regexp.MustCompile(`^[0-9]{6,20}$`)
	projectIDPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	networkNamePattern     = regexp.MustCompile(`^[a-z](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	spannerInstancePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}[a-z0-9]$`)
	spannerDatabasePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,28}[a-z0-9]$`)
)

// PrepoolSetupRequest is the body for provision, reclaim, and VSA cluster delete.
type PrepoolSetupRequest struct {
	DeploymentName        string                        `json:"deploymentId"`
	ConsumerNetwork       string                        `json:"network"`
	CustomerProjectNumber string                        `json:"projectId"`
	DBConfig              *prepoolinfra.SpannerDBConfig `json:"stateDbConfig,omitempty"`
}

func (h *Handler) handlePrepoolSetup(w http.ResponseWriter, r *http.Request) {
	req, err := decodePrepoolSetupRequest(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateSpannerEmulatorDBConfig(req.DBConfig); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, created := h.Jobs.CreateIfNoActive(JobOperationSetup, req)
	if !created {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("an active job already exists for deployment %s", req.DeploymentName))
		return
	}

	go h.runSetupJob(job.ID, req)

	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) handleReclaimDeployment(w http.ResponseWriter, r *http.Request) {
	req, err := decodePrepoolSetupRequest(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, created := h.Jobs.CreateIfNoActive(JobOperationReclaimDeployment, req)
	if !created {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("an active job already exists for deployment %s", req.DeploymentName))
		return
	}

	go h.runReclaimDeploymentJob(job.ID, req)

	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) handleReclaimAll(w http.ResponseWriter, r *http.Request) {
	req, err := decodePrepoolSetupRequest(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, created := h.Jobs.CreateIfNoActive(JobOperationReclaimAll, req)
	if !created {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("an active job already exists for deployment %s", req.DeploymentName))
		return
	}

	go h.runReclaimAllJob(job.ID, req)

	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) handleDeleteVSACluster(w http.ResponseWriter, r *http.Request) {
	req, err := decodePrepoolSetupRequest(w, r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, created := h.Jobs.CreateIfNoActive(JobOperationDeleteVSACluster, req)
	if !created {
		writeJSONError(w, http.StatusConflict, fmt.Sprintf("an active job already exists for deployment %s", req.DeploymentName))
		return
	}

	go h.runDeleteVSAClusterJob(job.ID, req)

	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) runSetupJob(jobID string, req PrepoolSetupRequest) {
	h.Jobs.SetRunning(jobID)
	ctx := h.Context

	provisioner, err := h.newProvisioner(ctx)
	if err != nil {
		slog.Error("Setup job failed to initialize provisioner", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	infra, err := provisioner.Setup(
		ctx,
		req.DeploymentName,
		req.ConsumerNetwork,
		req.CustomerProjectNumber,
		env.Region,
		req.DBConfig,
	)
	if err != nil {
		slog.Error("Setup job failed", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	slog.Info("Setup job completed", "jobID", jobID, "deploymentName", req.DeploymentName)
	h.Jobs.SetDone(jobID, &infra)
}

func (h *Handler) runReclaimDeploymentJob(jobID string, req PrepoolSetupRequest) {
	h.Jobs.SetRunning(jobID)
	ctx := h.Context

	provisioner, err := h.newProvisioner(ctx)
	if err != nil {
		slog.Error("Reclaim deployment job failed to initialize provisioner", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	err = provisioner.Reclaim(ctx, req.DeploymentName, req.ConsumerNetwork, req.CustomerProjectNumber, env.Region)
	if err != nil {
		slog.Error("Reclaim deployment job failed", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	slog.Info("Reclaim deployment job completed", "jobID", jobID)
	h.Jobs.SetDone(jobID, nil)
}

func (h *Handler) runReclaimAllJob(jobID string, req PrepoolSetupRequest) {
	h.Jobs.SetRunning(jobID)
	ctx := h.Context

	provisioner, err := h.newProvisioner(ctx)
	if err != nil {
		slog.Error("Reclaim all job failed to initialize provisioner", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	err = provisioner.ReclaimAll(ctx, req.DeploymentName, req.ConsumerNetwork, req.CustomerProjectNumber, env.Region)
	if err != nil {
		slog.Error("Reclaim all job failed", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	slog.Info("Reclaim all job completed", "jobID", jobID)
	h.Jobs.SetDone(jobID, nil)
}

func (h *Handler) runDeleteVSAClusterJob(jobID string, req PrepoolSetupRequest) {
	h.Jobs.SetRunning(jobID)
	ctx := h.Context

	provisioner, err := h.newProvisioner(ctx)
	if err != nil {
		slog.Error("Delete VSA cluster job failed to initialize provisioner", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	err = provisioner.DeleteVSACluster(ctx, req.DeploymentName, req.ConsumerNetwork, req.CustomerProjectNumber, env.Region)
	if err != nil {
		slog.Error("Delete VSA cluster job failed", "jobID", jobID, "error", err)
		h.Jobs.SetError(jobID, err.Error())
		return
	}

	slog.Info("Delete VSA cluster job completed", "jobID", jobID)
	h.Jobs.SetDone(jobID, nil)
}

func (h *Handler) newProvisioner(ctx context.Context) (prepoolinfra.Provisioner, error) {
	if h.ProvisionerFactory == nil {
		return nil, fmt.Errorf("provisioner factory is not configured")
	}
	provisioner, err := h.ProvisionerFactory(ctx)
	if err != nil {
		return nil, fmt.Errorf("create provisioner: %w", err)
	}
	if provisioner == nil {
		return nil, fmt.Errorf("create provisioner: factory returned nil")
	}
	return provisioner, nil
}

func decodePrepoolSetupRequest(w http.ResponseWriter, r *http.Request) (PrepoolSetupRequest, error) {
	var req PrepoolSetupRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return PrepoolSetupRequest{}, fmt.Errorf("invalid request body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return PrepoolSetupRequest{}, fmt.Errorf("invalid request body: only one JSON object is allowed")
	}
	if env.Region == "" {
		return PrepoolSetupRequest{}, fmt.Errorf("server configuration REGION is required")
	}
	if err := validatePrepoolSetupRequest(req); err != nil {
		return PrepoolSetupRequest{}, err
	}
	return req, nil
}

func validatePrepoolSetupRequest(req PrepoolSetupRequest) error {
	if err := names.ValidateDeploymentName(req.DeploymentName); err != nil {
		return err
	}
	if !projectNumberPattern.MatchString(req.CustomerProjectNumber) {
		return fmt.Errorf("projectId must be a 6-20 digit project number")
	}

	parts := strings.Split(req.ConsumerNetwork, "/")
	if len(parts) != 5 || parts[0] != "projects" || parts[2] != "global" || parts[3] != "networks" {
		return fmt.Errorf("network must use projects/{project}/global/networks/{network} format")
	}
	if !projectNumberPattern.MatchString(parts[1]) && !projectIDPattern.MatchString(parts[1]) {
		return fmt.Errorf("network contains an invalid project identifier")
	}
	if !networkNamePattern.MatchString(parts[4]) {
		return fmt.Errorf("network contains an invalid network name")
	}
	return nil
}

func validateSpannerEmulatorDBConfig(dbConfig *prepoolinfra.SpannerDBConfig) error {
	if !prepoolinfra.SpannerEmulatorEnabled() {
		return nil
	}
	if dbConfig == nil {
		return fmt.Errorf("dbConfig is required when SPANNER_EMULATOR_HOST is set")
	}
	if !projectNumberPattern.MatchString(dbConfig.Project) && !projectIDPattern.MatchString(dbConfig.Project) {
		return fmt.Errorf("dbConfig.project contains an invalid project identifier")
	}
	if !spannerInstancePattern.MatchString(dbConfig.Instance) {
		return fmt.Errorf("dbConfig.instance must be a valid 2-64 character Spanner instance ID")
	}
	if !spannerDatabasePattern.MatchString(dbConfig.Database) {
		return fmt.Errorf("dbConfig.database must be a valid 2-30 character Spanner database ID")
	}
	return nil
}
