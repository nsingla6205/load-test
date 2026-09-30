package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/tools/vcm-prepool/prepoolinfra"
)

// Handler serves VCM pre-pool HTTP endpoints.
type Handler struct {
	Context            context.Context
	ProvisionerFactory prepoolinfra.ProvisionerFactory
	Jobs               *JobStore
}

// NewHandler returns an HTTP handler with pre-pool routes registered.
func NewHandler(ctx context.Context, provisionerFactory prepoolinfra.ProvisionerFactory) http.Handler {
	h := &Handler{
		Context:            ctx,
		ProvisionerFactory: provisionerFactory,
		Jobs:               NewJobStore(),
	}
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

// Register attaches pre-pool routes onto mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("POST /prepoolSetup", h.handlePrepoolSetup)
	mux.HandleFunc("POST /prepoolSetup:reclaimDeployment", h.handleReclaimDeployment)
	mux.HandleFunc("POST /prepoolSetup:reclaimAll", h.handleReclaimAll)
	mux.HandleFunc("POST /prepoolSetup:deleteVsaCluster", h.handleDeleteVSACluster)
	mux.HandleFunc("GET /jobs/{jobId}", h.handleGetJob)
	mux.HandleFunc("GET /jobs", h.handleListJobs)
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}

func (h *Handler) handleGetJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobId")
	job := h.Jobs.Get(jobID)
	if job == nil {
		writeJSONError(w, http.StatusNotFound, "job not found")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (h *Handler) handleListJobs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.Jobs.List())
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
