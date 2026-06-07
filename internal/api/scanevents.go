package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/discovery"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
	"github.com/google/uuid"
)

// ScanEvent is one play-by-play event from a running (or replayed) discovery job.
// It is fanned out live over SSE and (for the meaningful stages) persisted for
// post-completion playback. The final discovery_results rows remain the source of
// truth; events are the narrative that produced them.
type ScanEvent struct {
	Seq      int64  `json:"seq"`
	JobID    string `json:"job_id"`
	IP       string `json:"ip,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	Stage    string `json:"stage"`
	Protocol string `json:"protocol,omitempty"`
	Status   string `json:"status,omitempty"`
	Message  string `json:"message,omitempty"`
	TS       string `json:"ts"`
}

// scanEventHub is an in-memory pub/sub for live scan events. Per job it keeps a
// capped replay buffer (so a late SSE subscriber catches up) and a set of
// subscriber channels. A subscriber that falls behind is dropped (its channel is
// closed) — the frontend then falls back to polling, so no event delivery is ever
// blocking on a slow client.
type scanEventHub struct {
	mu   sync.Mutex
	seq  int64
	jobs map[uuid.UUID]*jobStream
}

type jobStream struct {
	buf  []ScanEvent
	subs map[chan ScanEvent]struct{}
}

const (
	scanBufCap   = 4000
	scanSubDepth = 1024
	maxHubJobs   = 12
)

func newScanEventHub() *scanEventHub { return &scanEventHub{jobs: map[uuid.UUID]*jobStream{}} }

func (s *Server) hub() *scanEventHub {
	s.scanHubOnce.Do(func() { s.scanHub = newScanEventHub() })
	return s.scanHub
}

// publish assigns a monotonic seq, appends to the job's replay buffer, and fans
// the event out to all live subscribers (non-blocking; lagging subscribers are
// closed). Returns the seq-stamped event.
func (h *scanEventHub) publish(ev ScanEvent) ScanEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	ev.Seq = h.seq
	jid, _ := uuid.Parse(ev.JobID)
	js := h.jobs[jid]
	if js == nil {
		if len(h.jobs) >= maxHubJobs { // evict an arbitrary old job to bound memory
			for k := range h.jobs {
				delete(h.jobs, k)
				break
			}
		}
		js = &jobStream{subs: map[chan ScanEvent]struct{}{}}
		h.jobs[jid] = js
	}
	js.buf = append(js.buf, ev)
	if len(js.buf) > scanBufCap {
		js.buf = js.buf[len(js.buf)-scanBufCap:]
	}
	for c := range js.subs {
		select {
		case c <- ev:
		default: // subscriber too slow → drop it; client reconnects / polls
			delete(js.subs, c)
			close(c)
		}
	}
	return ev
}

// subscribe registers a new live subscriber and returns the current replay buffer
// (events so far) plus an unsubscribe func. The snapshot + channel are captured
// under one lock, so there is no gap or duplicate between them.
func (h *scanEventHub) subscribe(jobID uuid.UUID) (<-chan ScanEvent, []ScanEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	js := h.jobs[jobID]
	if js == nil {
		js = &jobStream{subs: map[chan ScanEvent]struct{}{}}
		h.jobs[jobID] = js
	}
	ch := make(chan ScanEvent, scanSubDepth)
	js.subs[ch] = struct{}{}
	snapshot := append([]ScanEvent(nil), js.buf...)
	unsub := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := js.subs[ch]; ok {
			delete(js.subs, ch)
		}
	}
	return ch, snapshot, unsub
}

// nonPersistedStages are live-only (the high-volume probing pulse, mostly dead
// IPs) — they drive the live board but add no value to playback history, so they
// are not written to discovery_job_events.
var nonPersistedStages = map[string]bool{
	"target_probe_started": true,
	"target_queued":        true,
}

// publishScanEvent stamps + fans out an event over the live hub and, for the
// meaningful stages, persists it for completed-job playback (best-effort).
func (s *Server) publishScanEvent(jobID uuid.UUID, ip netip.Addr, devID uuid.UUID, stage, protocol, status, message string) {
	ev := ScanEvent{
		JobID: jobID.String(), Stage: stage, Protocol: protocol, Status: status,
		Message: message, TS: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if ip.IsValid() {
		ev.IP = ip.String()
	}
	if devID != uuid.Nil {
		ev.DeviceID = devID.String()
	}
	s.hub().publish(ev)
	if nonPersistedStages[stage] {
		return
	}
	var ipPtr *netip.Addr
	if ip.IsValid() {
		ipPtr = &ip
	}
	var devPtr *uuid.UUID
	if devID != uuid.Nil {
		devPtr = &devID
	}
	pctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = s.queries.CreateDiscoveryJobEvent(pctx, db.CreateDiscoveryJobEventParams{
		JobID: jobID, Ip: ipPtr, DeviceID: devPtr, Stage: stage, Protocol: protocol, Status: status, Message: message,
	})
}

// pipelineEventEmitter returns an OnEvent callback bound to one host (job + ip),
// for the discovery pipeline to call as it probes that host.
func (s *Server) pipelineEventEmitter(jobID uuid.UUID, ip netip.Addr) func(discovery.PipelineEvent) {
	return func(ev discovery.PipelineEvent) {
		s.publishScanEvent(jobID, ip, uuid.Nil, ev.Stage, ev.Protocol, ev.Status, ev.Message)
	}
}

// eventFromRow maps a persisted discovery_job_events row to a ScanEvent (playback).
func eventFromRow(r db.ListDiscoveryJobEventsRow) ScanEvent {
	ev := ScanEvent{
		Seq: r.Seq, JobID: r.JobID.String(), Stage: r.Stage, Protocol: r.Protocol,
		Status: r.Status, Message: r.Message, TS: r.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if r.Ip != nil {
		ev.IP = r.Ip.String()
	}
	if r.DeviceID != nil {
		ev.DeviceID = r.DeviceID.String()
	}
	return ev
}

// listScanEvents handles GET /discovery/jobs/{id}/events — the persisted event
// history for completed-job playback (the live feed is the SSE /stream).
func (s *Server) listScanEvents(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	rows, err := s.queries.ListDiscoveryJobEvents(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]ScanEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, eventFromRow(row))
	}
	writeJSON(w, http.StatusOK, out)
}

// streamScanEvents handles GET /discovery/jobs/{id}/stream — Server-Sent Events.
// For a RUNNING job it replays the in-memory buffer then streams live; for a
// COMPLETED job it replays the persisted history and closes (playback). A slow
// client is dropped by the hub, after which the frontend falls back to polling.
func (s *Server) streamScanEvents(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := pathUUID(w, r, "id")
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx)

	send := func(ev ScanEvent) {
		b, _ := json.Marshal(ev)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}

	job, err := s.queries.GetDiscoveryJob(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}

	// Completed/failed → replay persisted history, then a terminal marker, and close.
	if job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled" {
		if rows, rerr := s.queries.ListDiscoveryJobEvents(ctx, id); rerr == nil {
			for _, row := range rows {
				send(eventFromRow(row))
			}
		}
		send(ScanEvent{JobID: id.String(), Stage: "job_completed", Status: job.Status, TS: time.Now().UTC().Format(time.RFC3339Nano)})
		flusher.Flush()
		return
	}

	// Running → subscribe (replay buffer + live), stream until done/disconnect.
	ch, snapshot, unsub := s.hub().subscribe(id)
	defer unsub()
	for _, ev := range snapshot {
		send(ev)
	}
	flusher.Flush()

	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open { // dropped by hub (lag) → client reconnects / falls back to polling
				return
			}
			send(ev)
			flusher.Flush()
			if ev.Stage == "job_completed" {
				return
			}
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
