package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Credential Test History read surface. Persisted by persistCredentialTest; these
// endpoints expose runs + per-result history for the Credential Testing page,
// Credential detail, and Device detail. Secrets are never present in this data.

type credTestRunDTO struct {
	ID         string `json:"id"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Actor      string `json:"actor"`
	Pairs      int32  `json:"pairs"`
	Successes  int32  `json:"successes"`
	Failures   int32  `json:"failures"`
}

type credTestResultDTO struct {
	ID             string `json:"id"`
	RunID          string `json:"run_id"`
	DeviceID       string `json:"device_id"`
	DeviceName     string `json:"device_name,omitempty"`
	CredentialID   string `json:"credential_id,omitempty"`
	CredentialName string `json:"credential_name"`
	Kind           string `json:"kind"`
	Protocol       string `json:"protocol"`
	Category       string `json:"category"`
	Success        bool   `json:"success"`
	Detail         string `json:"detail"`
	LatencyMS      int64  `json:"latency_ms"`
	TestedAt       string `json:"tested_at"`
	Actor          string `json:"actor"`
}

func uuidPtrStr(p *uuid.UUID) string {
	if p == nil {
		return ""
	}
	return p.String()
}

func qLimit(r *http.Request, def, max int32) int32 {
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && int32(n) <= max {
			return int32(n)
		}
	}
	return def
}

// listCredentialTestRuns — GET /credential-tests/runs
func (s *Server) listCredentialTestRuns(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListCredentialTestRuns(r.Context(), qLimit(r, 50, 500))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]credTestRunDTO, 0, len(rows))
	for _, x := range rows {
		d := credTestRunDTO{ID: x.ID.String(), StartedAt: x.StartedAt.Format(time.RFC3339), Actor: x.Actor, Pairs: x.Pairs, Successes: x.Successes, Failures: x.Failures}
		if x.FinishedAt != nil {
			d.FinishedAt = x.FinishedAt.Format(time.RFC3339)
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

// listCredentialTestRunResults — GET /credential-tests/runs/{id}/results
func (s *Server) listCredentialTestRunResults(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid run id", http.StatusBadRequest)
		return
	}
	rows, err := s.queries.ListCredentialTestResultsByRun(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]credTestResultDTO, 0, len(rows))
	for _, x := range rows {
		out = append(out, credTestResultDTO{
			ID: x.ID.String(), RunID: x.RunID.String(), DeviceID: x.DeviceID.String(),
			CredentialID: uuidPtrStr(x.CredentialID), CredentialName: x.CredentialName,
			Kind: x.Kind, Protocol: x.Protocol, Category: x.Category, Success: x.Success,
			Detail: x.Detail, LatencyMS: x.LatencyMs, TestedAt: x.TestedAt.Format(time.RFC3339), Actor: x.Actor,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// deviceCredentialTests — GET /devices/{id}/credential-tests
func (s *Server) deviceCredentialTests(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathDevice(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListDeviceCredentialTests(ctx, db.ListDeviceCredentialTestsParams{DeviceID: id, Limit: qLimit(r, 50, 500)})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]credTestResultDTO, 0, len(rows))
	for _, x := range rows {
		out = append(out, credTestResultDTO{
			ID: x.ID.String(), RunID: x.RunID.String(), DeviceID: x.DeviceID.String(),
			CredentialID: uuidPtrStr(x.CredentialID), CredentialName: x.CredentialName,
			Kind: x.Kind, Protocol: x.Protocol, Category: x.Category, Success: x.Success,
			Detail: x.Detail, LatencyMS: x.LatencyMs, TestedAt: x.TestedAt.Format(time.RFC3339), Actor: x.Actor,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// credentialCredentialTests — GET /credentials/{id}/credential-tests
func (s *Server) credentialCredentialTests(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid credential id", http.StatusBadRequest)
		return
	}
	rows, err := s.queries.ListCredentialCredentialTests(r.Context(), db.ListCredentialCredentialTestsParams{CredentialID: &id, Limit: qLimit(r, 50, 500)})
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]credTestResultDTO, 0, len(rows))
	for _, x := range rows {
		out = append(out, credTestResultDTO{
			ID: x.ID.String(), RunID: x.RunID.String(), DeviceID: x.DeviceID.String(), DeviceName: x.DeviceName,
			CredentialID: uuidPtrStr(x.CredentialID), CredentialName: x.CredentialName,
			Kind: x.Kind, Protocol: x.Protocol, Category: x.Category, Success: x.Success,
			Detail: x.Detail, LatencyMS: x.LatencyMs, TestedAt: x.TestedAt.Format(time.RFC3339), Actor: x.Actor,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
