package server

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/matrix"
)

type shareSlotResponse struct {
	SlotID       int     `json:"slotId"`
	Username     string  `json:"username"`
	Password     string  `json:"password"`
	Port         int     `json:"port"`
	Host         string  `json:"host"`
	Country      string  `json:"country,omitempty"`
	CountryShort string  `json:"countryShort,omitempty"`
	ASN          string  `json:"asn,omitempty"`
	ASNOrg       string  `json:"asnOrg,omitempty"`
	Source       string  `json:"source,omitempty"`
	State        string  `json:"state"`
	ExitIP       string  `json:"exitIp,omitempty"`
	SpeedBPS     int64   `json:"speedBps"`
	PingMS       int64   `json:"pingMs"`
	QualityScore float64 `json:"qualityScore"`
	Healthy      bool    `json:"healthy"`
	Residential  bool    `json:"residential"`
	Chained      bool    `json:"chained"`
	SOCKS5       string  `json:"socks5"`
	SOCKS5H      string  `json:"socks5h"`
	V2Ray        string  `json:"v2ray"`
}

// sharePool exposes the same slot inventory as the API-key pool, but through
// the panel session so the share page can build copyable client links.
func (s *Server) sharePool(c fiber.Ctx) error {
	if s.matrix == nil || !s.matrix.Enabled() {
		return writeError(c, fiber.StatusServiceUnavailable, "matrix is disabled")
	}
	statuses, ready := s.matrix.Status()
	settings := s.config.Get()
	host := shareSocksHost(settings.Matrix.PublicSocksHost, settings.Matrix.PublicHost, c.Get(fiber.HeaderHost))
	slots := make([]shareSlotResponse, 0, len(statuses))
	healthy := 0
	for _, status := range statuses {
		if status.Healthy {
			healthy++
		}
		slots = append(slots, shareSlot(host, settings.Matrix.Password, status))
	}
	limit := settings.Matrix.ShareLimit
	sort.Slice(slots, func(left, right int) bool {
		readyLeft := slots[left].State == "ready" && slots[left].Healthy
		readyRight := slots[right].State == "ready" && slots[right].Healthy
		if readyLeft != readyRight {
			return readyLeft
		}
		return slots[left].SlotID < slots[right].SlotID
	})
	if limit > 0 && len(slots) > limit {
		slots = slots[:limit]
	}
	return writeJSON(c, fiber.StatusOK, map[string]any{
		"enabled":    true,
		"host":       host,
		"direct":     strings.TrimSpace(settings.Matrix.PublicSocksHost) != "",
		"portStart":  settings.Matrix.PortStart,
		"maxSlots":   settings.Matrix.Slots,
		"shareLimit": limit,
		"total":      len(slots),
		"ready":      ready,
		"healthy":    healthy,
		"slots":      slots,
	})
}

func shareSlot(host, password string, status matrix.SlotStatus) shareSlotResponse {
	address := net.JoinHostPort(host, strconv.Itoa(status.Port))
	name := shareSlotName(status)
	return shareSlotResponse{
		SlotID:       status.SlotID,
		Username:     status.Username,
		Password:     password,
		Port:         status.Port,
		Host:         host,
		Country:      status.Country,
		CountryShort: status.CountryShort,
		ASN:          status.ASN,
		ASNOrg:       status.ASNOrg,
		Source:       status.Source,
		State:        status.State,
		ExitIP:       status.ExitIP,
		SpeedBPS:     status.SpeedBPS,
		PingMS:       status.PingMS,
		QualityScore: status.QualityScore,
		Healthy:      status.Healthy,
		Residential:  status.Residential,
		Chained:      status.Chained,
		SOCKS5:       socksLink("socks5", address, status.Username, password, name),
		SOCKS5H:      socksLink("socks5h", address, status.Username, password, name),
		V2Ray:        socksLink("socks", address, status.Username, password, name),
	}
}

func shareSlotName(status matrix.SlotStatus) string {
	country := strings.ToUpper(strings.TrimSpace(status.CountryShort))
	if country == "" {
		country = strings.ToUpper(strings.TrimSpace(status.Country))
	}
	if country == "" {
		country = "XX"
	}
	return fmt.Sprintf("ProxyGate-%02d-%s", status.SlotID, country)
}

func socksLink(scheme, address, username, password, name string) string {
	link := url.URL{
		Scheme:   scheme,
		User:     url.UserPassword(username, password),
		Host:     address,
		Fragment: name,
	}
	return link.String()
}

func shareSocksHost(publicSocksHost, publicHost, requestHost string) string {
	for _, candidate := range []string{publicSocksHost, publicHost, requestHost} {
		if host := normalizeShareHost(candidate); host != "" {
			return host
		}
	}
	return "127.0.0.1"
}

// normalizeShareHost accepts a configured URL/host/port and returns a dialable
// host. Instabox exposes panel hosts as "<port>-<name>", so the numeric prefix
// is removed when the configured value is the panel entry point.
func normalizeShareHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		if parsed, err := url.Parse(raw); err == nil {
			raw = parsed.Host
		}
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	raw = strings.Trim(raw, "[]")
	if index := strings.IndexByte(raw, '-'); index > 0 {
		if _, err := strconv.Atoi(raw[:index]); err == nil {
			raw = raw[index+1:]
		}
	}
	return raw
}
