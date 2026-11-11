package subscription

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ExpTechTW/proxygate/internal/matrix"
)

func (s *Server) handleSubscription(writer http.ResponseWriter, r *http.Request) {
	settings := s.config.Get().Matrix
	rest := strings.TrimPrefix(r.URL.Path, "/sub/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	matchedToken, client := settings.MatchSubscriptionToken(parts[0])
	if matchedToken == "" {
		http.NotFound(writer, r)
		return
	}
	format := ""
	if len(parts) > 1 {
		format = strings.ToLower(parts[1])
	}
	if format == "" {
		format = detectFormat(r.UserAgent())
	}

	statuses, _ := s.matrix.Status()
	host, port, tls := s.publicEndpoint(r)
	trojanPath := settings.SubscriptionTrojanPath
	if trojanPath == "" {
		trojanPath = "/pgtrojan"
	}
	options := nodeOptionsFromQuery(r.URL.Query())
	if client != nil {
		options = applyClientPresets(options, *client)
	}
	options.applyDefaults()
	if limit := settings.ShareLimit; limit > 0 && (options.count <= 0 || options.count > limit) {
		options.count = limit
	}
	statusesBySlot := make(map[int]matrix.SlotStatus, len(statuses))
	for _, status := range statuses {
		statusesBySlot[status.SlotID] = status
	}
	all := make([]trojanNode, 0, len(statuses))
	for _, status := range statuses {
		if status.State != "ready" || !status.Healthy || status.ExitIP == "" {
			continue
		}
		all = append(all, trojanNode{
			slotID:       status.SlotID,
			name:         nodeName(status),
			country:      countryCode(status),
			password:     slotUUID(matchedToken, status.SlotID),
			host:         host,
			port:         port,
			tls:          tls,
			path:         trojanPath,
			protocol:     status.Protocol,
			source:       status.Source,
			speedBps:     status.SpeedBPS,
			pingMs:       status.PingMS,
			quality:      status.QualityScore,
			failCount:    status.FailCount,
			successCount: status.SuccessCount,
		})
	}
	if len(all) > 0 {
		s.rememberNodes(all)
	} else if cached, ok := s.lastGoodNodes(); ok {
		all = cached
	}
	links := selectNodes(all, options)

	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Profile-Update-Interval", "6")
	writer.Header().Set("X-ProxyGate-Nodes", strconv.Itoa(len(links)))
	writer.Header().Set("X-ProxyGate-Format", format)
	if options.preset != "" {
		writer.Header().Set("X-ProxyGate-Preset", options.preset)
	}
	if len(options.slots) > 0 {
		writer.Header().Set("X-ProxyGate-Slots", strconv.Itoa(len(options.slots)))
	}
	if len(links) == 0 {
		writer.Header().Set("Retry-After", "60")
		message := "no ready slots yet"
		if len(all) > 0 {
			message = "no ready slots match the current filter"
		}
		http.Error(writer, message, http.StatusServiceUnavailable)
		return
	}
	socksHost := socksPublicHost(r, settings.PublicSocksHost, settings.PublicHost)
	switch format {
	case "socks", "socks5", "socks.txt", "socks5.txt":
		payload := socksList(links, statusesBySlot, socksHost, settings.Password, "socks5")
		if payload == "" {
			socksUnavailable(writer)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte(payload))
	case "socks5h", "socksh":
		payload := socksList(links, statusesBySlot, socksHost, settings.Password, "socks5h")
		if payload == "" {
			socksUnavailable(writer)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte(payload))
	case "socksb64", "socks-v2ray", "socks5b64", "socks5v2ray":
		payload := socksList(links, statusesBySlot, socksHost, settings.Password, "socks")
		if payload == "" {
			socksUnavailable(writer)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte(base64.StdEncoding.EncodeToString([]byte(payload))))
	case "sockscsv", "socks5csv":
		payload := socksCSV(links, statusesBySlot, socksHost, settings.Password)
		if payload == "" {
			socksUnavailable(writer)
			return
		}
		writer.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, _ = writer.Write([]byte(payload))
	case "clash", "mihomo", "yaml", "clash.yaml":
		writer.Header().Set("Content-Type", "text/yaml; charset=utf-8")
		_, _ = writer.Write([]byte(clashProfile(links, options)))
	case "singbox", "sing-box", "sb":
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = writer.Write([]byte(singboxProfile(links, options)))
	case "surge":
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte(surgeProfile(links, options)))
	case "quantumultx", "quantumult", "qx":
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte(quantumultXProfile(links, options)))
	case "v2ray", "v2rayn", "base64", "txt":
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte(v2raySubscription(links)))
	default:
		http.NotFound(writer, r)
	}
}

func detectFormat(agent string) string {
	agent = strings.ToLower(agent)
	for _, marker := range []string{"sing-box", "singbox"} {
		if strings.Contains(agent, marker) {
			return "singbox"
		}
	}
	if strings.Contains(agent, "surge") {
		return "surge"
	}
	if strings.Contains(agent, "quantumult") {
		return "qx"
	}
	for _, marker := range []string{"clash", "mihomo", "stash", "flclash"} {
		if strings.Contains(agent, marker) {
			return "clash"
		}
	}
	return "v2ray"
}

func (s *Server) publicEndpoint(r *http.Request) (string, int, bool) {
	settings := s.config.Get().Matrix
	host := strings.TrimSpace(settings.PublicHost)
	if host == "" {
		host = r.Host
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
	}
	host = strings.Trim(host, "[]")
	return host, 443, true
}

func countryCode(status matrix.SlotStatus) string {
	for _, candidate := range []string{status.CountryShort, status.Country} {
		code := strings.ToUpper(strings.TrimSpace(candidate))
		if len(code) == 2 {
			return code
		}
	}
	return "XX"
}

func nodeName(status matrix.SlotStatus) string {
	country := countryCode(status)
	organization := status.ASNOrg
	if index := strings.Index(organization, " - "); index >= 0 {
		organization = organization[:index]
	}
	organization = strings.Map(func(symbol rune) rune {
		switch {
		case symbol >= 'a' && symbol <= 'z', symbol >= 'A' && symbol <= 'Z', symbol >= '0' && symbol <= '9':
			return symbol
		default:
			return -1
		}
	}, organization)
	if organization == "" {
		organization = strings.TrimPrefix(status.ASN, "AS")
	}
	if organization == "" {
		organization = "node"
	}
	return fmt.Sprintf("pg-%02d-%s-%s", status.SlotID, country, organization)
}

// NodeName returns the display name used across subscription and local profiles.
func NodeName(status matrix.SlotStatus) string { return nodeName(status) }

// NodeCountry returns the two-letter country code used in node names.
func NodeCountry(status matrix.SlotStatus) string { return countryCode(status) }

type trojanNode struct {
	slotID       int
	name         string
	country      string
	password     string
	host         string
	port         int
	tls          bool
	path         string
	protocol     string
	source       string
	speedBps     int64
	pingMs       int64
	quality      float64
	failCount    int
	successCount int
}

func (n trojanNode) link() string {
	query := url.Values{}
	query.Set("type", "ws")
	query.Set("host", n.host)
	query.Set("path", n.path)
	if n.tls {
		query.Set("security", "tls")
		query.Set("sni", n.host)
		query.Set("fp", "chrome")
	} else {
		query.Set("security", "none")
	}
	return "trojan://" + url.QueryEscape(n.password) + "@" + net.JoinHostPort(n.host, strconv.Itoa(n.port)) + "?" + query.Encode() + "#" + url.PathEscape(n.name)
}

func v2raySubscription(nodes []trojanNode) string {
	if len(nodes) == 0 {
		return ""
	}
	lines := make([]string, 0, len(nodes))
	for _, node := range nodes {
		lines = append(lines, node.link())
	}
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))
}

// socksList renders the selected slots as client-importable SOCKS URIs. v2rayN
// expects the socks:// scheme, while browser automation tools usually accept
// socks5:// directly.
func socksList(links []trojanNode, statuses map[int]matrix.SlotStatus, host, password, scheme string) string {
	lines := make([]string, 0, len(links))
	for _, node := range links {
		status, ok := statuses[node.slotID]
		if !ok || status.Username == "" || status.Port == 0 {
			continue
		}
		link := url.URL{
			Scheme:   scheme,
			User:     url.UserPassword(status.Username, password),
			Host:     net.JoinHostPort(host, strconv.Itoa(status.Port)),
			Fragment: node.name,
		}
		lines = append(lines, link.String())
	}
	return strings.Join(lines, "\n")
}

func socksCSV(links []trojanNode, statuses map[int]matrix.SlotStatus, host, password string) string {
	var builder strings.Builder
	builder.WriteString("host,port,username,password,country,slot,name\n")
	for _, node := range links {
		status, ok := statuses[node.slotID]
		if !ok || status.Username == "" || status.Port == 0 {
			continue
		}
		fmt.Fprintf(&builder, "%s,%d,%s,%s,%s,%d,%s\n", host, status.Port, status.Username, password, node.country, node.slotID, node.name)
	}
	return builder.String()
}

func socksUnavailable(writer http.ResponseWriter) {
	writer.Header().Set("Retry-After", "60")
	http.Error(writer, "no ready socks slots", http.StatusServiceUnavailable)
}

func socksPublicHost(r *http.Request, publicSocksHost, publicHost string) string {
	for _, candidate := range []string{publicSocksHost, publicHost, r.Host} {
		if host := normalizeSocksHost(candidate); host != "" {
			return host
		}
	}
	return "127.0.0.1"
}

func normalizeSocksHost(raw string) string {
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

// rememberNodes keeps the last non-empty node set so a subscription request
// during a pool rebuild (container restart, slot re-allocation) still returns
// a usable profile instead of an empty one. Slot credentials are derived from
// the token and slot id, so cached entries stay valid across refreshes.
func (s *Server) rememberNodes(nodes []trojanNode) {
	if len(nodes) == 0 {
		return
	}
	snapshot := append([]trojanNode(nil), nodes...)
	s.cacheMu.Lock()
	s.cachedNodes = snapshot
	s.cachedAt = time.Now()
	s.cacheMu.Unlock()
}

func (s *Server) lastGoodNodes() ([]trojanNode, bool) {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if len(s.cachedNodes) == 0 {
		return nil, false
	}
	return append([]trojanNode(nil), s.cachedNodes...), true
}

func clashProfile(nodes []trojanNode, options nodeOptions) string {
	var builder strings.Builder
	builder.WriteString("mixed-port: 7890\n")
	builder.WriteString("allow-lan: false\n")
	builder.WriteString("mode: rule\n")
	builder.WriteString("log-level: info\n")
	builder.WriteString("ipv6: false\n")
	builder.WriteString("unified-delay: true\n")
	builder.WriteString("tcp-concurrent: true\n")
	builder.WriteString("proxies:\n")
	if len(nodes) == 0 {
		builder.WriteString("  - name: \"direct\"\n    type: direct\n")
	}
	for _, node := range nodes {
		builder.WriteString("  - name: " + strconv.Quote(node.name) + "\n")
		builder.WriteString("    type: trojan\n")
		builder.WriteString("    server: " + strconv.Quote(node.host) + "\n")
		builder.WriteString("    port: " + strconv.Itoa(node.port) + "\n")
		builder.WriteString("    password: " + strconv.Quote(node.password) + "\n")
		builder.WriteString("    udp: true\n")
		if node.tls {
			builder.WriteString("    sni: " + strconv.Quote(node.host) + "\n")
			builder.WriteString("    skip-cert-verify: false\n")
			builder.WriteString("    client-fingerprint: chrome\n")
		}
		builder.WriteString("    network: ws\n")
		builder.WriteString("    ws-opts:\n")
		builder.WriteString("      path: " + strconv.Quote(node.path) + "\n")
		builder.WriteString("      headers:\n")
		builder.WriteString("        Host: " + strconv.Quote(node.host) + "\n")
	}

	countryGroups := countryGroupsFor(nodes)
	includeCountryGroups := options.group != "auto" && options.group != "fallback" && options.group != "none"
	builder.WriteString("proxy-groups:\n")
	builder.WriteString("  - name: ProxyGate\n")
	builder.WriteString("    type: select\n")
	builder.WriteString("    proxies:\n")
	writeGroupEntry := func(name string) {
		builder.WriteString("      - " + strconv.Quote(name) + "\n")
	}
	nodeNames := make([]string, 0, len(nodes))
	for _, node := range nodes {
		nodeNames = append(nodeNames, node.name)
	}
	if len(nodes) == 0 {
		writeGroupEntry("direct")
	} else {
		switch options.group {
		case "auto":
			writeGroupEntry("Auto")
			writeGroupEntry("PG-Fallback")
		case "fallback":
			writeGroupEntry("PG-Fallback")
			writeGroupEntry("Auto")
		case "none":
			// Only individual nodes are selectable.
		default:
			writeGroupEntry("Auto")
			writeGroupEntry("PG-Fallback")
			for _, group := range countryGroups {
				writeGroupEntry(group.name)
			}
		}
		for _, name := range nodeNames {
			writeGroupEntry(name)
		}
	}
	builder.WriteString("  - name: Auto\n")
	builder.WriteString("    type: url-test\n")
	builder.WriteString("    url: \"https://www.gstatic.com/generate_204\"\n")
	builder.WriteString("    interval: 300\n")
	builder.WriteString("    tolerance: 50\n")
	builder.WriteString("    proxies:\n")
	if len(nodes) == 0 {
		builder.WriteString("      - \"direct\"\n")
	}
	for _, name := range nodeNames {
		builder.WriteString("      - " + strconv.Quote(name) + "\n")
	}
	builder.WriteString("  - name: PG-Fallback\n")
	builder.WriteString("    type: fallback\n")
	builder.WriteString("    url: \"https://www.gstatic.com/generate_204\"\n")
	builder.WriteString("    interval: 300\n")
	builder.WriteString("    proxies:\n")
	if len(nodes) == 0 {
		builder.WriteString("      - \"direct\"\n")
	}
	for _, name := range nodeNames {
		builder.WriteString("      - " + strconv.Quote(name) + "\n")
	}
	if includeCountryGroups {
		for _, group := range countryGroups {
			builder.WriteString("  - name: " + strconv.Quote(group.name) + "\n")
			builder.WriteString("    type: url-test\n")
			builder.WriteString("    url: \"https://www.gstatic.com/generate_204\"\n")
			builder.WriteString("    interval: 300\n")
			builder.WriteString("    tolerance: 50\n")
			builder.WriteString("    proxies:\n")
			for _, name := range group.nodes {
				builder.WriteString("      - " + strconv.Quote(name) + "\n")
			}
		}
	}
	builder.WriteString("dns:\n")
	builder.WriteString("  enable: true\n")
	builder.WriteString("  ipv6: false\n")
	builder.WriteString("  enhanced-mode: fake-ip\n")
	builder.WriteString("  fake-ip-range: 198.18.0.1/16\n")
	builder.WriteString("  default-nameserver:\n")
	builder.WriteString("    - 223.5.5.5\n")
	builder.WriteString("    - 1.1.1.1\n")
	builder.WriteString("  nameserver:\n")
	builder.WriteString("    - https://1.1.1.1/dns-query\n")
	builder.WriteString("    - https://8.8.8.8/dns-query\n")
	builder.WriteString("rules:\n")
	builder.WriteString("  - MATCH,ProxyGate\n")
	return builder.String()
}

type countryGroup struct {
	name  string
	nodes []string
}

func countryGroupsFor(nodes []trojanNode) []countryGroup {
	index := make(map[string]int)
	groups := make([]countryGroup, 0, 8)
	for _, node := range nodes {
		code := strings.ToUpper(node.country)
		if len(code) != 2 {
			code = "XX"
		}
		position, exists := index[code]
		if !exists {
			position = len(groups)
			index[code] = position
			groups = append(groups, countryGroup{name: "PG-" + code})
		}
		groups[position].nodes = append(groups[position].nodes, node.name)
	}
	return groups
}

// singboxProfile renders a sing-box JSON profile. It mirrors the Clash groups:
// a selector with a latency-based Auto group and a priority Fallback group.
func singboxProfile(nodes []trojanNode, options nodeOptions) string {
	nodeTags := make([]string, 0, len(nodes))
	outbounds := make([]any, 0, len(nodes)+3)
	for _, node := range nodes {
		nodeTags = append(nodeTags, node.name)
		outbound := map[string]any{
			"type":        "trojan",
			"tag":         node.name,
			"server":      node.host,
			"server_port": node.port,
			"password":    node.password,
		}
		if node.tls {
			outbound["tls"] = map[string]any{"enabled": true, "server_name": node.host}
		}
		outbound["transport"] = map[string]any{
			"type":    "ws",
			"path":    node.path,
			"headers": map[string]any{"Host": node.host},
		}
		outbounds = append(outbounds, outbound)
	}
	selectorMembers := nodeTags
	switch options.group {
	case "none":
	case "fallback":
		selectorMembers = append([]string{"PG-Fallback", "Auto"}, nodeTags...)
	default:
		selectorMembers = append([]string{"Auto", "PG-Fallback"}, nodeTags...)
	}
	if len(selectorMembers) == 0 {
		selectorMembers = []string{"direct"}
		outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	}
	head := []any{map[string]any{
		"type":      "selector",
		"tag":       "ProxyGate",
		"outbounds": selectorMembers,
		"default":   selectorMembers[0],
	}}
	if options.group != "none" && len(nodeTags) > 0 {
		outbounds = append(outbounds,
			map[string]any{"type": "urltest", "tag": "Auto", "outbounds": nodeTags, "url": "https://www.gstatic.com/generate_204", "interval": "5m", "tolerance": 50},
			map[string]any{"type": "urltest", "tag": "PG-Fallback", "outbounds": nodeTags, "url": "https://www.gstatic.com/generate_204", "interval": "5m"},
		)
	}
	profile := map[string]any{
		"log": map[string]any{"level": "info"},
		"inbounds": []any{
			map[string]any{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080},
		},
		"outbounds": append(head, outbounds...),
		"route": map[string]any{
			"rules": []any{},
			"final": "ProxyGate",
		},
	}
	encoded, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(encoded) + "\n"
}

// surgeProfile renders a Surge configuration with individual trojan
// WebSocket proxies plus Auto (url-test) and PG-Fallback groups.
func surgeProfile(nodes []trojanNode, options nodeOptions) string {
	names := make([]string, 0, len(nodes))
	var builder strings.Builder
	builder.WriteString("[General]\n")
	builder.WriteString("loglevel = notify\n")
	builder.WriteString("dns-server = 223.5.5.5, 1.1.1.1\n")
	builder.WriteString("ipv6 = false\n\n[Proxy]\n")
	for _, node := range nodes {
		names = append(names, node.name)
		builder.WriteString(node.name + " = trojan, " + node.host + ", " + strconv.Itoa(node.port) +
			", password=" + node.password)
		if node.tls {
			builder.WriteString(", sni=" + node.host + ", skip-cert-verify=false")
		}
		builder.WriteString(", ws=true, ws-path=" + node.path + ", ws-headers=Host:" + node.host + "\n")
	}
	builder.WriteString("\n[Proxy Group]\n")
	if len(names) > 0 {
		builder.WriteString("Auto = url-test, " + strings.Join(names, ", ") + ", url=https://www.gstatic.com/generate_204, interval=300, tolerance=50\n")
		builder.WriteString("PG-Fallback = fallback, " + strings.Join(names, ", ") + ", url=https://www.gstatic.com/generate_204, interval=300\n")
	}
	members := names
	switch options.group {
	case "none":
	case "fallback":
		members = append([]string{"PG-Fallback", "Auto"}, names...)
	default:
		members = append([]string{"Auto", "PG-Fallback"}, names...)
	}
	if len(members) == 0 {
		members = []string{"DIRECT"}
	}
	builder.WriteString("ProxyGate = select, " + strings.Join(members, ", ") + "\n\n[Rule]\nFINAL,ProxyGate\n")
	return builder.String()
}

// quantumultXProfile renders a Quantumult X server/policy section with a
// latency benchmark group.
func quantumultXProfile(nodes []trojanNode, options nodeOptions) string {
	names := make([]string, 0, len(nodes))
	var builder strings.Builder
	builder.WriteString("[general]\n")
	builder.WriteString("server_check_url=http://www.gstatic.com/generate_204\n\n[server_local]\n")
	for _, node := range nodes {
		names = append(names, node.name)
		builder.WriteString("trojan=" + net.JoinHostPort(node.host, strconv.Itoa(node.port)) +
			", password=" + node.password + ", over-tls=true, tls-host=" + node.host +
			", obfs=wss, obfs-host=" + node.host + ", obfs-uri=" + node.path +
			", udp-relay=true, tag=" + node.name + "\n")
	}
	builder.WriteString("\n[policy]\n")
	if len(names) > 0 && options.group != "none" {
		builder.WriteString("url-latency-benchmark=Auto, " + strings.Join(names, ", ") + ", check-interval=300\n")
	}
	members := names
	if options.group != "none" && len(names) > 0 {
		members = append([]string{"Auto"}, names...)
	}
	if len(members) == 0 {
		members = []string{"direct"}
	}
	builder.WriteString("static=ProxyGate, " + strings.Join(members, ", ") + "\n\n[filter_local]\nfinal, ProxyGate\n")
	return builder.String()
}
