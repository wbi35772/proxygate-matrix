package server

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/config"
)

type frontProxySettings struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	Slot    int    `json:"slot"`
	API     string `json:"api"`
	Timeout string `json:"timeout"`
}

func frontProxySettingsFrom(settings config.Matrix) frontProxySettings {
	return frontProxySettings{
		Enabled: settings.FrontProxyEnabled,
		Type:    settings.FrontProxyType,
		URL:     settings.FrontProxyURL,
		Slot:    settings.FrontProxySlot,
		API:     settings.FrontProxyAPI,
		Timeout: settings.FrontProxyTimeout,
	}
}

func (s *Server) getFrontProxy(c fiber.Ctx) error {
	settings := s.config.Get()
	return writeJSON(c, fiber.StatusOK, frontProxySettingsFrom(settings.Matrix))
}

func (s *Server) putFrontProxy(c fiber.Ctx) error {
	var input frontProxySettings
	if !decodeJSON(c, &input) {
		return nil
	}
	kind := strings.ToLower(strings.TrimSpace(input.Type))
	switch kind {
	case "", "socks5", "socks5h", "http", "https", "list", "slot", "api":
	default:
		return writeError(c, fiber.StatusBadRequest, "front proxy type must be socks5, http, https, list, slot or api")
	}
	timeout := strings.TrimSpace(input.Timeout)
	if timeout == "" {
		timeout = "15s"
	}
	if parsed, err := time.ParseDuration(timeout); err != nil || parsed <= 0 {
		return writeError(c, fiber.StatusBadRequest, "front proxy timeout must be a Go duration such as 15s")
	}
	if input.Enabled {
		switch kind {
		case "slot":
			if input.Slot < 1 {
				return writeError(c, fiber.StatusBadRequest, "front proxy slot must be at least 1")
			}
			if input.Slot > s.config.Get().Matrix.Slots {
				return writeError(c, fiber.StatusBadRequest, "front proxy slot exceeds the configured slot count")
			}
		case "api":
			if strings.TrimSpace(input.API) == "" {
				return writeError(c, fiber.StatusBadRequest, "front proxy API URL is required")
			}
		default:
			if strings.TrimSpace(input.URL) == "" {
				return writeError(c, fiber.StatusBadRequest, "front proxy URL is required")
			}
		}
	}
	current := s.config.Get()
	current.Matrix.FrontProxyEnabled = input.Enabled
	current.Matrix.FrontProxyType = kind
	current.Matrix.FrontProxyURL = strings.TrimSpace(input.URL)
	current.Matrix.FrontProxySlot = input.Slot
	current.Matrix.FrontProxyAPI = strings.TrimSpace(input.API)
	current.Matrix.FrontProxyTimeout = timeout
	if err := s.config.Save(current); err != nil {
		return writeError(c, fiber.StatusBadRequest, err.Error())
	}
	s.logger.Printf("[server] front proxy updated enabled=%t type=%s", input.Enabled, kind)
	return writeJSON(c, fiber.StatusOK, frontProxySettingsFrom(s.config.Get().Matrix))
}
