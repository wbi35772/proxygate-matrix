package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/matrix"
)

type poolSlotResponse struct {
	matrix.SlotStatus
	SOCKS5 string `json:"socks5"`
}

func (s *Server) poolAuth(c fiber.Ctx) error {
	if s.matrix == nil || !s.matrix.Enabled() {
		return writeError(c, fiber.StatusServiceUnavailable, "matrix is disabled")
	}
	key := s.config.Get().Matrix.APIKey
	provided := c.Get(fiber.HeaderAuthorization)
	provided = strings.TrimSpace(strings.TrimPrefix(provided, "Bearer "))
	if key == "" || subtle.ConstantTimeCompare([]byte(key), []byte(provided)) != 1 {
		return writeError(c, fiber.StatusUnauthorized, "invalid API key")
	}
	return c.Next()
}

func (s *Server) matrixDialStats(c fiber.Ctx) error {
	if s.matrix == nil {
		return writeError(c, fiber.StatusServiceUnavailable, "matrix is disabled")
	}
	return writeJSON(c, fiber.StatusOK, s.matrix.DialStats())
}

func (s *Server) poolSlots(c fiber.Ctx) error {
	statuses, _ := s.matrix.Status()
	settings := s.config.Get()
	host := s.socksHost()
	slots := make([]poolSlotResponse, 0, len(statuses))
	for index := range statuses {
		slots = append(slots, s.poolSlot(host, statuses[index]))
	}
	return writeJSON(c, fiber.StatusOK, map[string]any{
		"enabled":   true,
		"maxSlots":  settings.Matrix.Slots,
		"available": max(0, settings.Matrix.Slots-len(statuses)),
		"slots":     slots,
	})
}

func (s *Server) poolAllocate(c fiber.Ctx) error {
	options := matrix.AllocateOptions{}
	if len(c.Body()) > 0 {
		if !decodeJSON(c, &options) {
			return nil
		}
	}
	slot, created, err := s.matrix.Allocate(options)
	if err != nil {
		switch {
		case errors.Is(err, matrix.ErrDisabled):
			return writeError(c, fiber.StatusServiceUnavailable, err.Error())
		case errors.Is(err, matrix.ErrInvalid):
			return writeError(c, fiber.StatusBadRequest, err.Error())
		default:
			return writeError(c, fiber.StatusInternalServerError, err.Error())
		}
	}
	status := 200
	if created {
		status = fiber.StatusCreated
	}
	return writeJSON(c, status, map[string]any{
		"created": created,
		"slot":    s.poolSlot(s.socksHost(), slot.Status()),
	})
}

func (s *Server) poolRotate(c fiber.Ctx) error {
	slotID, err := strconv.Atoi(c.Params("slot_id"))
	if err != nil || slotID < 1 {
		return writeError(c, fiber.StatusBadRequest, "slot_id must be a positive integer")
	}
	if err := s.matrix.Rotate(slotID); err != nil {
		switch {
		case errors.Is(err, matrix.ErrNotFound):
			return writeError(c, fiber.StatusNotFound, err.Error())
		case errors.Is(err, matrix.ErrBusy):
			return writeError(c, fiber.StatusConflict, err.Error())
		default:
			return writeError(c, fiber.StatusInternalServerError, err.Error())
		}
	}
	statuses, _ := s.matrix.Status()
	for _, status := range statuses {
		if status.SlotID == slotID {
			return writeJSON(c, fiber.StatusAccepted, map[string]any{"slot": s.poolSlot(s.socksHost(), status)})
		}
	}
	return writeJSON(c, fiber.StatusAccepted, map[string]any{"slotId": slotID, "state": matrix.StateSwitching})
}

func (s *Server) poolStats(c fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stats, err := s.matrix.Stats(ctx)
	if err != nil {
		return writeError(c, fiber.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, fiber.StatusOK, stats)
}

func (s *Server) socksHost() string {
	if host := s.config.Get().Matrix.PublicSocksHost; host != "" {
		return host
	}
	return "127.0.0.1"
}

type poolCountry struct {
	Code  string `json:"code"`
	Total int    `json:"total"`
}

// matrixSlots is the session-authenticated view of the pool used by the panel.
// It intentionally omits SOCKS credentials; allocation and rotation still go
// through the same matrix manager as the API-key pool endpoints.
func (s *Server) matrixSlots(c fiber.Ctx) error {
	if s.matrix == nil || !s.matrix.Enabled() {
		return writeError(c, fiber.StatusServiceUnavailable, "matrix is disabled")
	}
	statuses, ready := s.matrix.Status()
	settings := s.config.Get()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stats, err := s.matrix.Stats(ctx)
	if err != nil {
		return writeError(c, fiber.StatusInternalServerError, err.Error())
	}
	countries := make([]poolCountry, 0, len(stats.Pool.ByCountry))
	for code, total := range stats.Pool.ByCountry {
		if len(code) != 2 {
			continue
		}
		countries = append(countries, poolCountry{Code: code, Total: total})
	}
	sort.Slice(countries, func(left, right int) bool {
		if countries[left].Total != countries[right].Total {
			return countries[left].Total > countries[right].Total
		}
		return countries[left].Code < countries[right].Code
	})
	return writeJSON(c, fiber.StatusOK, map[string]any{
		"enabled":   true,
		"maxSlots":  settings.Matrix.Slots,
		"available": max(0, settings.Matrix.Slots-len(statuses)),
		"ready":     ready,
		"healthy":   stats.Healthy,
		"slots":     statuses,
		"countries": countries,
	})
}

func (s *Server) matrixConfigureSlot(c fiber.Ctx) error {
	slotID, err := strconv.Atoi(c.Params("slot_id"))
	if err != nil || slotID < 1 {
		return writeError(c, fiber.StatusBadRequest, "slot_id must be a positive integer")
	}
	options := matrix.AllocateOptions{SlotID: slotID}
	if len(c.Body()) > 0 {
		if !decodeJSON(c, &options) {
			return nil
		}
		options.SlotID = slotID
	}
	slot, created, err := s.matrix.Allocate(options)
	if err != nil {
		switch {
		case errors.Is(err, matrix.ErrDisabled):
			return writeError(c, fiber.StatusServiceUnavailable, err.Error())
		case errors.Is(err, matrix.ErrInvalid):
			return writeError(c, fiber.StatusBadRequest, err.Error())
		default:
			return writeError(c, fiber.StatusInternalServerError, err.Error())
		}
	}
	status := fiber.StatusOK
	if created {
		status = fiber.StatusCreated
	}
	return writeJSON(c, status, map[string]any{"created": created, "slot": slot.Status()})
}

type matrixCountryRequest struct {
	Country  string `json:"country"`
	From     int    `json:"from"`
	Count    int    `json:"count"`
	Protocol string `json:"protocol"`
	Mode     string `json:"mode"`
}

// matrixConfigureCountry assigns one country to a contiguous block of slots.
// It is the bulk control behind the panel's "check a country, get that
// country's nodes" workflow.
func (s *Server) matrixConfigureCountry(c fiber.Ctx) error {
	request := matrixCountryRequest{}
	if !decodeJSON(c, &request) {
		return nil
	}
	if request.From == 0 {
		request.From = 1
	}
	if request.Count == 0 {
		request.Count = 1
	}
	settings := s.config.Get()
	if request.From < 1 || request.Count < 1 || request.From+request.Count-1 > settings.Matrix.Slots {
		return writeError(c, fiber.StatusBadRequest, "slot range is outside the configured pool")
	}
	updated := 0
	failures := make([]string, 0)
	for id := request.From; id < request.From+request.Count; id++ {
		options := matrix.AllocateOptions{SlotID: id, Country: request.Country, Protocol: request.Protocol, Mode: request.Mode}
		if _, _, err := s.matrix.Allocate(options); err != nil {
			failures = append(failures, fmt.Sprintf("slot %d: %v", id, err))
			continue
		}
		updated++
	}
	status := fiber.StatusOK
	if len(failures) > 0 && updated == 0 {
		status = fiber.StatusBadRequest
	}
	return writeJSON(c, status, map[string]any{
		"updated":  updated,
		"failed":   len(failures),
		"failures": failures,
		"country":  strings.ToUpper(strings.TrimSpace(request.Country)),
		"from":     request.From,
		"count":    request.Count,
	})
}

func (s *Server) poolSlot(host string, status matrix.SlotStatus) poolSlotResponse {
	settings := s.config.Get()
	password := settings.Matrix.Password
	credential := url.UserPassword(status.Username, password)
	address := net.JoinHostPort(host, strconv.Itoa(status.Port))
	return poolSlotResponse{SlotStatus: status, SOCKS5: (&url.URL{Scheme: "socks5", User: credential, Host: address}).String()}
}
