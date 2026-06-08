package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coralsearesorts/hims/internal/netflow"
	"github.com/coralsearesorts/hims/internal/storage/postgres/db"
)

// NetFlow Analytics (#12). A UDP collector receives NetFlow v5 exports, decodes
// them, and aggregates a rolling window in memory; a flush ticker writes the
// top talkers / protocol mix / top conversations to flow_records. The analytics
// API rolls those aggregates up over a recent window. Devices must be
// configured to export NetFlow v5 to this collector's address for data to flow.

// flowCollector holds the in-memory rolling aggregation between flushes.
type flowCollector struct {
	mu      sync.Mutex
	summary *netflow.Summary
	packets uint64
}

// StartFlowCollector listens for NetFlow v5 on addr (e.g. ":2055"), decodes +
// aggregates packets, and flushes top-N aggregates to the DB every flush
// interval. It runs until ctx is cancelled. A bind failure is logged and the
// rest of the API still serves.
func (s *Server) StartFlowCollector(ctx context.Context, addr string, flush time.Duration) {
	s.flowAddr = addr
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		slog.Warn("netflow collector disabled: cannot bind", "addr", addr, "error", err)
		s.flowAddr = ""
		return
	}
	fc := &flowCollector{summary: netflow.NewSummary()}
	s.flow = fc

	// Reader loop.
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			_, flows, derr := netflow.Decode(buf[:n])
			if derr != nil {
				continue // not a v5 packet / malformed — ignore
			}
			fc.mu.Lock()
			fc.summary.Add(flows)
			fc.packets++
			fc.mu.Unlock()
		}
	}()

	// Flush loop.
	go func() {
		t := time.NewTicker(flush)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
			s.flushFlows(ctx, fc)
		}
	}()
	slog.Info("netflow v5 collector listening", "addr", addr)
}

// flushFlows snapshots and resets the rolling summary, then persists the top
// aggregates. A no-traffic window writes nothing.
func (s *Server) flushFlows(ctx context.Context, fc *flowCollector) {
	fc.mu.Lock()
	sum := fc.summary
	fc.summary = netflow.NewSummary()
	fc.mu.Unlock()
	if sum.Total.Bytes == 0 {
		return
	}
	write := func(kind string, entries []netflow.Entry) {
		for _, e := range entries {
			if err := s.queries.InsertFlowRecord(ctx, db.InsertFlowRecordParams{
				Kind: kind, Label: e.Label, Bytes: int64(e.Bytes), Packets: int64(e.Packets),
			}); err != nil && ctx.Err() == nil {
				slog.Warn("flow record insert failed", "kind", kind, "error", err)
				return
			}
		}
	}
	write("talker", netflow.TopN(sum.ByHost, 20))
	write("protocol", netflow.TopN(sum.ByProtocol, 0))
	write("conversation", netflow.TopN(sum.ByConversation, 20))
}

// flowWindow returns the cutoff for "recent" analytics (query ?window=minutes).
func flowWindow(r *http.Request) time.Time {
	mins := 15
	if v := r.URL.Query().Get("window"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1440 {
			mins = n
		}
	}
	return time.Now().UTC().Add(-time.Duration(mins) * time.Minute)
}

func flowLimit(r *http.Request, def int) int32 {
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			return int32(n)
		}
	}
	return int32(def)
}

func (s *Server) flowOverview(w http.ResponseWriter, r *http.Request) {
	ov, err := s.queries.FlowOverview(r.Context(), flowWindow(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	var packetsSeen uint64
	if s.flow != nil {
		s.flow.mu.Lock()
		packetsSeen = s.flow.packets
		s.flow.mu.Unlock()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"listening":        s.flowAddr != "",
		"listen_addr":      s.flowAddr,
		"bytes":            ov.Bytes,
		"packets":          ov.Packets,
		"talkers":          ov.Talkers,
		"last_at":          ov.LastAt,
		"packets_received": packetsSeen,
	})
}

func (s *Server) flowTopTalkers(w http.ResponseWriter, r *http.Request) {
	s.flowEntries(w, r, "talker", flowLimit(r, 20))
}
func (s *Server) flowProtocols(w http.ResponseWriter, r *http.Request) {
	s.flowEntries(w, r, "protocol", 50)
}
func (s *Server) flowConversations(w http.ResponseWriter, r *http.Request) {
	s.flowEntries(w, r, "conversation", flowLimit(r, 20))
}

func (s *Server) flowEntries(w http.ResponseWriter, r *http.Request, kind string, limit int32) {
	rows, err := s.queries.TopFlowEntries(r.Context(), db.TopFlowEntriesParams{Kind: kind, At: flowWindow(r), Limit: limit})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
