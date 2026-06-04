package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coralsearesorts/hims/internal/notify"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// notifChannelDTO is the safe, metadata-only view of a channel — the encrypted
// target (webhook URL, bot token, SMTP password) is NEVER returned. A redacted
// hint (non-secret bits only) is shown for recognizability.
type notifChannelDTO struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Type          string  `json:"type"`
	MinSeverity   string  `json:"min_severity"`
	Enabled       bool    `json:"enabled"`
	QuietStart    *string `json:"quiet_start,omitempty"`
	QuietEnd      *string `json:"quiet_end,omitempty"`
	TargetHint    string  `json:"target_hint"`
	CreatedAt     string  `json:"created_at"`
}

func minToHHMM(m *int32) *string {
	if m == nil {
		return nil
	}
	s := fmt.Sprintf("%02d:%02d", *m/60, *m%60)
	return &s
}

func hhmmToMin(s *string) (*int32, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	parts := strings.SplitN(*s, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("time must be HH:MM")
	}
	h, e1 := strconv.Atoi(parts[0])
	mi, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil || h < 0 || h > 23 || mi < 0 || mi > 59 {
		return nil, fmt.Errorf("invalid HH:MM")
	}
	v := int32(h*60 + mi)
	return &v, nil
}

// targetHint derives a non-secret recognizable label from a decrypted target.
func targetHint(chType string, t notify.Target) string {
	switch chType {
	case "slack", "teams", "webhook":
		if i := strings.Index(t.URL, "://"); i >= 0 {
			rest := t.URL[i+3:]
			if j := strings.IndexByte(rest, '/'); j >= 0 {
				return rest[:j] // host only — never the secret path/token
			}
			return rest
		}
		return "configured"
	case "telegram":
		return "chat " + t.ChatID
	case "email":
		return strings.Join(t.To, ", ")
	}
	return "configured"
}

func (s *Server) channelDTO(ctx context.Context, c db.NotificationChannel) notifChannelDTO {
	d := notifChannelDTO{
		ID: c.ID.String(), Name: c.Name, Type: c.Type, MinSeverity: c.MinSeverity,
		Enabled: c.Enabled, QuietStart: minToHHMM(c.QuietStartMin), QuietEnd: minToHHMM(c.QuietEndMin),
		CreatedAt: c.CreatedAt.Format(time.RFC3339), TargetHint: "(locked)",
	}
	if cph := s.cipher(); cph != nil {
		if plain, err := cph.Open(c.TargetEncrypted, c.KeyID); err == nil {
			var t notify.Target
			if json.Unmarshal(plain, &t) == nil {
				d.TargetHint = targetHint(c.Type, t)
			}
		}
	}
	return d
}

func (s *Server) listNotificationChannels(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListNotificationChannels(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]notifChannelDTO, 0, len(rows))
	for _, c := range rows {
		out = append(out, s.channelDTO(r.Context(), c))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createNotificationChannel(w http.ResponseWriter, r *http.Request) {
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not loaded; unlock it before adding notification channels", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Name        string        `json:"name"`
		Type        string        `json:"type"`
		MinSeverity string        `json:"min_severity"`
		QuietStart  *string       `json:"quiet_start"`
		QuietEnd    *string       `json:"quiet_end"`
		Target      notify.Target `json:"target"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" || req.Type == "" {
		http.Error(w, "name and type are required", http.StatusBadRequest)
		return
	}
	qs, err := hhmmToMin(req.QuietStart)
	if err != nil {
		http.Error(w, "quiet_start: "+err.Error(), http.StatusBadRequest)
		return
	}
	qe, err := hhmmToMin(req.QuietEnd)
	if err != nil {
		http.Error(w, "quiet_end: "+err.Error(), http.StatusBadRequest)
		return
	}
	plain, _ := json.Marshal(req.Target)
	blob, keyID, err := cph.Seal(plain)
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.queries.CreateNotificationChannel(r.Context(), db.CreateNotificationChannelParams{
		Name: req.Name, Type: req.Type, TargetEncrypted: blob, KeyID: keyID,
		MinSeverity: orDefault(req.MinSeverity, "warning"), QuietStartMin: qs, QuietEndMin: qe, Enabled: true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "monitoring", "notification.channel.create", "notification_channel", c.ID.String(), "Created a notification channel", map[string]any{"type": c.Type})
	writeJSON(w, http.StatusCreated, s.channelDTO(r.Context(), c))
}

func (s *Server) setNotificationChannelEnabled(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	var req setEnabledReq
	if !decodeJSON(w, r, &req) {
		return
	}
	c, err := s.queries.SetNotificationChannelEnabled(ctx, db.SetNotificationChannelEnabledParams{ID: id, Enabled: req.Enabled})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.channelDTO(ctx, c))
}

func (s *Server) deleteNotificationChannel(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	if err := s.queries.DeleteNotificationChannel(ctx, id); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "monitoring", "notification.channel.delete", "notification_channel", id.String(), "Deleted a notification channel", nil)
	w.WriteHeader(http.StatusNoContent)
}

// testNotificationChannel sends a one-off test message through the channel.
func (s *Server) testNotificationChannel(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	cph := s.cipher()
	if cph == nil {
		http.Error(w, "encryption key not loaded", http.StatusServiceUnavailable)
		return
	}
	c, err := s.queries.GetNotificationChannel(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	plain, err := cph.Open(c.TargetEncrypted, c.KeyID)
	if err != nil {
		writeErr(w, err)
		return
	}
	var t notify.Target
	if err := json.Unmarshal(plain, &t); err != nil {
		writeErr(w, err)
		return
	}
	sendErr := notify.Send(ctx, c.Type, t, "HIMS test notification", "This is a test message from HIMS confirming the channel works.")
	status, detail := "test", "Test message sent."
	if sendErr != nil {
		status, detail = "failed", sendErr.Error() // transport error only; never the secret
	}
	_, _ = s.queries.InsertNotificationLog(ctx, db.InsertNotificationLogParams{ChannelID: id, Status: status, Detail: detail})
	writeJSON(w, http.StatusOK, map[string]any{"ok": sendErr == nil, "detail": detail})
}

func (s *Server) listNotificationLog(w http.ResponseWriter, r *http.Request) {
	rows, err := s.queries.ListNotificationLog(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// ---- Background dispatcher --------------------------------------------------

// StartNotifier sends notifications for new/escalated alerts to matching
// channels on a tick. No-op while the encryption key is locked (targets can't
// be decrypted) or no channels exist.
func (s *Server) StartNotifier(ctx context.Context, tick time.Duration) {
	go func() {
		t := time.NewTimer(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.dispatchNotifications(ctx)
			t.Reset(tick)
		}
	}()
}

func (s *Server) dispatchNotifications(ctx context.Context) {
	cph := s.cipher()
	if cph == nil {
		return
	}
	channels, err := s.queries.ListNotificationChannels(ctx)
	if err != nil || len(channels) == 0 {
		return
	}
	alerts, err := s.queries.ListNotifiableAlerts(ctx)
	if err != nil || len(alerts) == 0 {
		return
	}
	sent, _ := s.queries.ListSentNotificationPairs(ctx)
	sentSet := make(map[string]bool, len(sent))
	for _, p := range sent {
		if p.AlertID != nil {
			sentSet[p.ChannelID.String()+"|"+p.AlertID.String()] = true
		}
	}
	nowMin := time.Now().Hour()*60 + time.Now().Minute()

	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		plain, derr := cph.Open(ch.TargetEncrypted, ch.KeyID)
		if derr != nil {
			continue
		}
		var tgt notify.Target
		if json.Unmarshal(plain, &tgt) != nil {
			continue
		}
		quiet := notify.InQuietHours(ch.QuietStartMin, ch.QuietEndMin, nowMin)
		for _, a := range alerts {
			if sentSet[ch.ID.String()+"|"+a.ID.String()] {
				continue
			}
			if !notify.ShouldNotify(a.Severity, ch.MinSeverity, quiet) {
				continue
			}
			subject := fmt.Sprintf("[%s] HIMS alert", strings.ToUpper(a.Severity))
			body := a.Message
			if a.Escalated {
				body += "\n(ESCALATED — still unacknowledged)"
			}
			status, detail := "sent", ""
			if serr := notify.Send(ctx, ch.Type, tgt, subject, body); serr != nil {
				status, detail = "failed", serr.Error()
			}
			aid := a.ID
			if _, lerr := s.queries.InsertNotificationLog(ctx, db.InsertNotificationLogParams{
				ChannelID: ch.ID, AlertID: &aid, Status: status, Detail: detail,
			}); lerr != nil {
				slog.Warn("notify: log insert failed", "channel", ch.ID, "error", lerr)
			}
			if status == "sent" {
				sentSet[ch.ID.String()+"|"+a.ID.String()] = true
			}
		}
	}
}
