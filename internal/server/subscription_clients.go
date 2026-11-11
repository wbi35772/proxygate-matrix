package server

import (
	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/config"
)

// putSubscriptionClients replaces the per-client subscription token list. The
// client token doubles as the Trojan/VLESS credential seed, so removing an
// entry revokes that client's profile at the next connection attempt.
func (s *Server) putSubscriptionClients(c fiber.Ctx) error {
	var input struct {
		Clients []config.SubscriptionClient `json:"clients"`
	}
	if !decodeJSON(c, &input) {
		return nil
	}
	if input.Clients == nil {
		input.Clients = []config.SubscriptionClient{}
	}
	settings := s.config.Get()
	settings.Matrix.SubscriptionClients = input.Clients
	if err := s.config.Save(settings); err != nil {
		return writeError(c, fiber.StatusBadRequest, err.Error())
	}
	current := s.config.Get()
	host := subscriptionPublicHost(c, current.Matrix, current.Web.ListenAddress)
	return writeJSON(c, fiber.StatusOK, fiber.Map{
		"clients": s.subscriptionClientViews(current.Matrix, host),
	})
}
