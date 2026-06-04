package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/coralsearesorts/hims/internal/notify"
	"github.com/coralsearesorts/hims/internal/reports"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// Reports Pro (#21) — scheduled email delivery.

func (s *Server) listReportSchedules(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListReportSchedules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

type createReportScheduleReq struct {
	Name       string  `json:"name"`
	ReportType string  `json:"report_type"`
	ChannelID  *string `json:"channel_id"`
	Frequency  string  `json:"frequency"`
	HourUTC    int32   `json:"hour_utc"`
}

func (s *Server) createReportSchedule(w http.ResponseWriter, r *http.Request) {
	var req createReportScheduleReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	row, err := s.queries.CreateReportSchedule(r.Context(), db.CreateReportScheduleParams{
		Name:       req.Name,
		ReportType: orDefault(req.ReportType, "inventory"),
		ChannelID:  parseUUIDPtr(req.ChannelID),
		Frequency:  orDefault(req.Frequency, "weekly"),
		HourUtc:    req.HourUTC,
		Enabled:    true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "report.schedule.create", "report_schedule", row.ID.String(), "Created report schedule "+row.Name, nil)
	writeJSON(w, http.StatusCreated, row)
}

func (s *Server) setReportScheduleEnabled(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	row, err := s.queries.SetReportScheduleEnabled(ctx, db.SetReportScheduleEnabledParams{ID: id, Enabled: req.Enabled})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) deleteReportSchedule(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteReportSchedule(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "config", "report.schedule.delete", "report_schedule", id.String(), "Deleted report schedule", nil)
	w.WriteHeader(http.StatusNoContent)
}

// runReportScheduleNow handles POST /report-schedules/{id}/run — generate +
// deliver immediately, returning the outcome.
func (s *Server) runReportScheduleNow(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	sched, err := s.queries.GetReportSchedule(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := s.deliverSchedule(ctx, sched)
	_ = s.queries.RecordReportScheduleRun(ctx, db.RecordReportScheduleRunParams{ID: id, LastStatus: status})
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// deliverSchedule builds the schedule's report and, if it has an email channel
// and the key is unlocked, emails the summary. Returns a short status string
// recorded as last_status. Never returns secrets.
func (s *Server) deliverSchedule(ctx context.Context, sched db.ReportSchedule) string {
	rep, err := s.buildReport(ctx, sched.ReportType, time.Now().UTC())
	if err != nil {
		return "generate failed: " + err.Error()
	}
	if sched.ChannelID == nil {
		return fmt.Sprintf("generated (%d sheets); no channel — not sent", len(rep.Sheets))
	}
	cph := s.cipher()
	if cph == nil {
		return "generated; key locked — not sent"
	}
	ch, err := s.queries.GetNotificationChannel(ctx, *sched.ChannelID)
	if err != nil {
		return "generated; channel missing — not sent"
	}
	plain, err := cph.Open(ch.TargetEncrypted, ch.KeyID)
	if err != nil {
		return "generated; channel decrypt failed"
	}
	var t notify.Target
	if err := json.Unmarshal(plain, &t); err != nil {
		return "generated; channel target invalid"
	}
	body := rep.Summary() + "\nThe full report is available in HIMS → Reports → Export.\n"
	if err := notify.Send(ctx, ch.Type, t, "HIMS Report: "+sched.Name, body); err != nil {
		return "send failed: " + err.Error() // transport only, never the secret
	}
	return "sent"
}

// StartReportScheduler runs due report schedules on a tick. Like the notifier,
// it is a no-op while the key is locked (channels can't be decrypted).
func (s *Server) StartReportScheduler(ctx context.Context, tick time.Duration) {
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			rows, err := s.queries.ListReportSchedules(ctx)
			if err != nil {
				continue
			}
			now := time.Now().UTC()
			for _, sched := range rows {
				if !sched.Enabled || !reports.IsDue(sched.Frequency, int(sched.HourUtc), sched.LastRunAt, now) {
					continue
				}
				status := s.deliverSchedule(ctx, sched)
				if err := s.queries.RecordReportScheduleRun(ctx, db.RecordReportScheduleRunParams{ID: sched.ID, LastStatus: status}); err != nil && ctx.Err() == nil {
					slog.Warn("report schedule run record failed", "schedule", sched.Name, "error", err)
				}
				slog.Info("report schedule ran", "schedule", sched.Name, "status", status)
			}
		}
	}()
}
