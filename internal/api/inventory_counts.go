package api

import "net/http"

// deviceCategoryCounts is GET /devices/category-counts — a {category: count} map over all
// non-deleted devices. The sidebar inventory groups and the data-driven group/category
// pages both derive their counts from this single source, so a sidebar count always
// reconciles with the rows on the page it links to (a group count = sum of its categories).
func (s *Server) deviceCategoryCounts(w http.ResponseWriter, r *http.Request) {
	devs, err := s.queries.ListAllDevices(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	counts := map[string]int{}
	for _, d := range devs {
		counts[d.Category]++
	}
	writeJSON(w, http.StatusOK, counts)
}
