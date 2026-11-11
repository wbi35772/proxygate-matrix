package server

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/matrix"
)

type subscriptionNode struct {
	SlotID       int     `json:"slotId"`
	Name         string  `json:"name"`
	Country      string  `json:"country,omitempty"`
	ASNOrg       string  `json:"asnOrg,omitempty"`
	Protocol     string  `json:"protocol,omitempty"`
	Source       string  `json:"source,omitempty"`
	SpeedBPS     int64   `json:"speedBps"`
	PingMS       int64   `json:"pingMs"`
	QualityScore float64 `json:"qualityScore"`
	FailCount    int     `json:"failCount"`
	SuccessCount int     `json:"successCount"`
	Residential  bool    `json:"residential"`
	Chained      bool    `json:"chained"`
}

type subscriptionInfo struct {
	Enabled       bool                    `json:"enabled"`
	V2RayURL      string                  `json:"v2rayUrl"`
	ClashURL      string                  `json:"clashUrl"`
	AutoURL       string                  `json:"autoUrl"`
	SingBoxURL    string                  `json:"singboxUrl"`
	SurgeURL      string                  `json:"surgeUrl"`
	QXURL         string                  `json:"qxUrl"`
	MetricsURL    string                  `json:"metricsUrl"`
	PublicHost    string                  `json:"publicHost"`
	Token         string                  `json:"token"`
	WebSocketPath string                  `json:"websocketPath"`
	TrojanPath    string                  `json:"trojanPath"`
	Protocol      string                  `json:"protocol"`
	UDP           bool                    `json:"udp"`
	Slots         map[string]int          `json:"slots"`
	Countries     map[string]int          `json:"countries"`
	PoolCountries map[string]int          `json:"poolCountries"`
	Nodes         []subscriptionNode      `json:"nodes"`
	Clients       []subscriptionClient    `json:"clients"`
	FrontProxy    frontProxySettings      `json:"frontProxy"`
}

type subscriptionClient struct {
	Name      string   `json:"name"`
	Token     string   `json:"token"`
	Count     int      `json:"count"`
	Countries []string `json:"countries"`
	Exclude   []string `json:"exclude"`
	Shuffle   bool     `json:"shuffle"`
	Group     string   `json:"group"`
	Sort      string   `json:"sort"`
	V2RayURL  string   `json:"v2rayUrl"`
	ClashURL  string   `json:"clashUrl"`
	AutoURL   string   `json:"autoUrl"`
	SingBoxURL string  `json:"singboxUrl"`
	SurgeURL  string   `json:"surgeUrl"`
	QXURL     string   `json:"qxUrl"`
}

// subscriptionClientViews renders the per-client token list with ready-made
// subscription URLs for the panel.
func (s *Server) subscriptionClientViews(settings config.Matrix, host string) []subscriptionClient {
	clients := make([]subscriptionClient, 0, len(settings.SubscriptionClients))
	for _, client := range settings.SubscriptionClients {
		view := subscriptionClient{
			Name:      client.Name,
			Token:     client.Token,
			Count:     client.Count,
			Countries: append([]string{}, client.Countries...),
			Exclude:   append([]string{}, client.Exclude...),
			Shuffle:   client.Shuffle,
			Group:     client.Group,
			Sort:      client.Sort,
		}
		if host != "" && client.Token != "" {
			base := "https://" + host + "/sub/" + url.PathEscape(client.Token)
			view.V2RayURL = base + "/v2ray"
			view.ClashURL = base + "/clash"
			view.AutoURL = base
			view.SingBoxURL = base + "/singbox"
			view.SurgeURL = base + "/surge"
			view.QXURL = base + "/qx"
		}
		clients = append(clients, view)
	}
	return clients
}

func (s *Server) subscription(c fiber.Ctx) error {
	current := s.config.Get()
	settings := current.Matrix
	slots := map[string]int{"total": 0, "ready": 0, "healthy": 0, "switching": 0, "error": 0}
	countries := map[string]int{}
	statuses, _ := s.matrix.Status()
	nodes := make([]subscriptionNode, 0, len(statuses))
	for _, status := range statuses {
		slots["total"]++
		switch status.State {
		case "ready":
			slots["ready"]++
			if !status.Healthy {
				break
			}
			slots["healthy"]++
			code := strings.ToUpper(status.CountryShort)
			if len(code) != 2 {
				code = strings.ToUpper(status.Country)
			}
			if len(code) == 2 {
				countries[code]++
			}
			nodes = append(nodes, subscriptionNode{
				SlotID:       status.SlotID,
				Name:         slotLabel(status),
				Country:      code,
				ASNOrg:       status.ASNOrg,
				Protocol:     status.Protocol,
				Source:       status.Source,
				SpeedBPS:     status.SpeedBPS,
				PingMS:       status.PingMS,
				QualityScore: status.QualityScore,
				FailCount:    status.FailCount,
				SuccessCount: status.SuccessCount,
				Residential:  status.Residential,
				Chained:      status.Chained,
			})
		case "switching":
			slots["switching"]++
		default:
			slots["error"]++
		}
	}

	poolCountries := map[string]int{}
	statsCtx, statsCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if stats, err := s.matrix.Stats(statsCtx); err == nil {
		for code, total := range stats.Pool.ByCountry {
			code = strings.ToUpper(strings.TrimSpace(code))
			if len(code) == 2 {
				poolCountries[code] = total
			}
		}
	}
	statsCancel()

	token := settings.SubscriptionTokenValue()
	host := subscriptionPublicHost(c, settings, current.Web.ListenAddress)
	websocketPath := settings.SubscriptionPath
	if websocketPath == "" {
		websocketPath = "/pgws"
	}
	trojanPath := settings.SubscriptionTrojanPath
	if trojanPath == "" {
		trojanPath = "/pgtrojan"
	}

	info := subscriptionInfo{
		Enabled:       strings.TrimSpace(settings.SubscriptionListen) != "",
		PublicHost:    host,
		Token:         token,
		WebSocketPath: websocketPath,
		TrojanPath:    trojanPath,
		Protocol:      "trojan-ws",
		UDP:           true,
		Slots:         slots,
		Countries:     countries,
		PoolCountries: poolCountries,
		Nodes:         nodes,
		Clients:       s.subscriptionClientViews(settings, host),
		FrontProxy:    frontProxySettingsFrom(settings),
	}
	if host != "" && token != "" {
		base := "https://" + host + "/sub/" + url.PathEscape(token)
		info.V2RayURL = base + "/v2ray"
		info.ClashURL = base + "/clash"
		info.AutoURL = base
		info.SingBoxURL = base + "/singbox"
		info.SurgeURL = base + "/surge"
		info.QXURL = base + "/qx"
		info.MetricsURL = "https://" + host + "/metrics"
	}
	return writeJSON(c, fiber.StatusOK, info)
}

// subscriptionPublicHost resolves the host clients use to fetch the
// subscription. The configured public host wins; otherwise the panel request
// host is adapted to the subscription listener (including the Instabox
// "PORT-name" edge style where the port is part of the hostname).
func subscriptionPublicHost(c fiber.Ctx, settings config.Matrix, webListen string) string {
	if host := strings.TrimSpace(settings.PublicHost); host != "" {
		return strings.Trim(host, "[]")
	}
	requestHost := strings.TrimSpace(c.Get(fiber.HeaderHost))
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHost = host
	}
	requestHost = strings.Trim(requestHost, "[]")
	subPort := listenPort(settings.SubscriptionListen)
	webPort := listenPort(webListen)
	if requestHost == "" || subPort == "" {
		return ""
	}
	if webPort != "" {
		if parts := strings.SplitN(requestHost, "-", 2); len(parts) == 2 && parts[0] == webPort {
			return subPort + "-" + parts[1]
		}
	}
	if strings.Contains(requestHost, ":") {
		return requestHost
	}
	return net.JoinHostPort(requestHost, subPort)
}

func listenPort(address string) string {
	if _, port, err := net.SplitHostPort(address); err == nil {
		return port
	}
	if strings.HasPrefix(address, ":") {
		return strings.TrimPrefix(address, ":")
	}
	return ""
}

func slotLabel(status matrix.SlotStatus) string {
	country := strings.ToUpper(status.CountryShort)
	if len(country) != 2 {
		country = strings.ToUpper(status.Country)
	}
	if len(country) != 2 {
		country = "XX"
	}
	label := "pg-" + fmt.Sprintf("%02d", status.SlotID) + "-" + country
	if status.ASNOrg != "" {
		suffix := strings.ToLower(strings.Map(func(symbol rune) rune {
			if symbol >= 'a' && symbol <= 'z' || symbol >= '0' && symbol <= '9' {
				return symbol
			}
			if symbol >= 'A' && symbol <= 'Z' {
				return symbol + ('a' - 'A')
			}
			return '-'
		}, status.ASNOrg))
		suffix = strings.Trim(strings.Join(strings.FieldsFunc(suffix, func(symbol rune) bool { return symbol == '-' }), "-"), "-")
		if len(suffix) > 18 {
			suffix = suffix[:18]
		}
		if suffix != "" {
			label += "-" + suffix
		}
	}
	return label
}
