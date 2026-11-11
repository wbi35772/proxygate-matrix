// Package subscription turns allocated matrix slots into VLESS-over-WebSocket
// nodes and publishes v2rayN/Clash subscriptions for them. Instabox terminates
// TLS at its edge, so the WebSocket listener is plain HTTP and the subscription
// links point at the public HTTPS endpoint.
package subscription

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/matrix"
)

type Server struct {
	config *config.Store
	matrix *matrix.Manager
	logger *log.Logger

	cacheMu     sync.RWMutex
	cachedNodes []trojanNode
	cachedAt    time.Time

	httpServer *http.Server
	listener   net.Listener
}

func New(configStore *config.Store, matrixManager *matrix.Manager, logger *log.Logger) *Server {
	return &Server{
		config: configStore,
		matrix: matrixManager,
		logger: logger,
	}
}

// subscriptionTokens lists the primary token plus every per-client token.
func (s *Server) subscriptionTokens() []string {
	settings := s.config.Get().Matrix
	tokens := make([]string, 0, len(settings.SubscriptionClients)+1)
	tokens = append(tokens, settings.SubscriptionTokenValue())
	for _, client := range settings.SubscriptionClients {
		if token := strings.TrimSpace(client.Token); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// slotID resolves a VLESS uuid to a slot. The mapping is computed on demand so
// adding or revoking a client token takes effect without a restart.
func (s *Server) slotID(uuid string) (int, bool) {
	settings := s.config.Get().Matrix
	for _, token := range s.subscriptionTokens() {
		for id := 1; id <= settings.Slots; id++ {
			if slotUUID(token, id) == uuid {
				return id, true
			}
		}
	}
	return 0, false
}

func (s *Server) trojanSlotID(key [trojanKeyLength]byte) (int, bool) {
	settings := s.config.Get().Matrix
	for _, token := range s.subscriptionTokens() {
		for id := 1; id <= settings.Slots; id++ {
			if trojanKey(slotUUID(token, id)) == key {
				return id, true
			}
		}
	}
	return 0, false
}
