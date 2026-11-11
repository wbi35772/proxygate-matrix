package subscription

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

func (s *Server) Start() error {
	settings := s.config.Get().Matrix
	if settings.SubscriptionListen == "" {
		return errors.New("subscription listener is disabled")
	}
	listener, err := net.Listen("tcp", settings.SubscriptionListen)
	if err != nil {
		return fmt.Errorf("listen subscription on %s: %w", settings.SubscriptionListen, err)
	}
	path := settings.SubscriptionPath
	if path == "" {
		path = "/pgws"
	}
	trojanPath := settings.SubscriptionTrojanPath
	if trojanPath == "" {
		trojanPath = "/pgtrojan"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(writer, "ok\n")
	})
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc(path, s.handleVLESS)
	mux.HandleFunc(trojanPath, s.handleTrojan)
	mux.HandleFunc("/sub/", s.handleSubscription)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 15 * time.Second,
	}
	s.listener = listener
	s.httpServer = server
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Printf("[subscription] server stopped: %v", err)
		}
	}()
	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	server := s.httpServer
	s.httpServer = nil
	s.listener = nil
	return server.Shutdown(ctx)
}

func isWebSocketRequest(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, value := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(value), "upgrade") {
			return true
		}
	}
	return false
}
