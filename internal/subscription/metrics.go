package subscription

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ExpTechTW/proxygate/internal/matrix"
)

// handleMetrics exposes a Prometheus text-format snapshot of slot and pool
// health. It makes the 90% availability target scrapeable and alertable
// instead of relying on the panel alone.
func (s *Server) handleMetrics(writer http.ResponseWriter, request *http.Request) {
	statuses, ready := s.matrix.Status()
	healthy := 0
	byCountry := make(map[string]int)
	for _, status := range statuses {
		if status.State != matrix.StateReady || !status.Healthy {
			continue
		}
		healthy++
		code := strings.ToUpper(status.CountryShort)
		if len(code) != 2 {
			code = strings.ToUpper(status.Country)
		}
		if len(code) == 2 {
			byCountry[code]++
		}
	}
	settings := s.config.Get().Matrix

	var builder strings.Builder
	builder.WriteString("# HELP proxygate_matrix_slots_total Allocated matrix slots.\n")
	builder.WriteString("# TYPE proxygate_matrix_slots_total gauge\n")
	fmt.Fprintf(&builder, "proxygate_matrix_slots_total %d\n", len(statuses))
	builder.WriteString("# HELP proxygate_matrix_slots_configured Configured slot capacity.\n")
	builder.WriteString("# TYPE proxygate_matrix_slots_configured gauge\n")
	fmt.Fprintf(&builder, "proxygate_matrix_slots_configured %d\n", settings.Slots)
	builder.WriteString("# HELP proxygate_matrix_slots_ready Slots reporting the ready state.\n")
	builder.WriteString("# TYPE proxygate_matrix_slots_ready gauge\n")
	fmt.Fprintf(&builder, "proxygate_matrix_slots_ready %d\n", ready)
	builder.WriteString("# HELP proxygate_matrix_slots_healthy Slots ready and passing the data-plane probe.\n")
	builder.WriteString("# TYPE proxygate_matrix_slots_healthy gauge\n")
	fmt.Fprintf(&builder, "proxygate_matrix_slots_healthy %d\n", healthy)
	builder.WriteString("# HELP proxygate_matrix_slots_healthy_by_country Healthy slots per country.\n")
	builder.WriteString("# TYPE proxygate_matrix_slots_healthy_by_country gauge\n")
	codes := make([]string, 0, len(byCountry))
	for code := range byCountry {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		fmt.Fprintf(&builder, "proxygate_matrix_slots_healthy_by_country{country=%q} %d\n", code, byCountry[code])
	}

	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	if stats, err := s.matrix.Stats(ctx); err == nil {
		builder.WriteString("# HELP proxygate_matrix_pool_nodes Candidate nodes in the pool.\n")
		builder.WriteString("# TYPE proxygate_matrix_pool_nodes gauge\n")
		fmt.Fprintf(&builder, "proxygate_matrix_pool_nodes %d\n", stats.Pool.Total)
		builder.WriteString("# HELP proxygate_matrix_pool_nodes_by_country Candidate nodes per country.\n")
		builder.WriteString("# TYPE proxygate_matrix_pool_nodes_by_country gauge\n")
		poolCodes := make([]string, 0, len(stats.Pool.ByCountry))
		for code := range stats.Pool.ByCountry {
			poolCodes = append(poolCodes, code)
		}
		sort.Strings(poolCodes)
		for _, code := range poolCodes {
			if len(code) != 2 {
				continue
			}
			fmt.Fprintf(&builder, "proxygate_matrix_pool_nodes_by_country{country=%q} %d\n", code, stats.Pool.ByCountry[code])
		}
	}

	writer.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(builder.String()))
}
