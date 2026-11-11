package vpngate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ExpTechTW/proxygate/internal/model"
)

// ParseSource dispatches on the payload shape so a single refresh loop can mix
// VPN Gate CSV, monosans/proxifly JSON, and plain host:port lists.
func ParseSource(data []byte, sourceURL string, refreshedAt time.Time) ([]model.Node, error) {
	trimmed := bytes.TrimPrefix(bytes.TrimSpace(data), []byte("\xef\xbb\xbf"))
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("source %s returned an empty body", sourceURL)
	}
	prefix := trimmed
	if len(prefix) > 8192 {
		prefix = prefix[:8192]
	}
	if trimmed[0] == '[' || trimmed[0] == '{' {
		nodes, err := parseProxiesJSON(trimmed, refreshedAt)
		if err == nil {
			return nodes, nil
		}
		if bytes.Contains(prefix, []byte("#HostName,")) {
			return ParseCSV(string(trimmed), refreshedAt)
		}
		return nil, err
	}
	if bytes.Contains(prefix, []byte("#HostName,")) || bytes.HasPrefix(trimmed, []byte("HostName,")) {
		return ParseCSV(string(trimmed), refreshedAt)
	}
	return ParseProxyList(string(trimmed), sourceURL, refreshedAt)
}

// proxyEntry covers the community JSON layouts:
//   - monosans: {protocol, host, port, timeout, asn, geolocation.country{iso_code,names.en}}
//   - proxifly: {proxy, protocol, ip, port, geolocation.country:"US"}
//   - vakhov:   {host, ip, port:"4145", delay, country_code, country_name, http:"1", socks5:"0"}
type proxyEntry struct {
	Protocol    string     `json:"protocol"`
	Host        string     `json:"host"`
	IP          string     `json:"ip"`
	Proxy       string     `json:"proxy"`
	Port        proxyPort  `json:"port"`
	Timeout     float64    `json:"timeout"`
	Delay       int64      `json:"delay"`
	ExitIP      string     `json:"exit_ip"`
	CountryCode string     `json:"country_code"`
	CountryName string     `json:"country_name"`
	HTTP        json.RawMessage `json:"http"`
	Socks5      json.RawMessage `json:"socks5"`
	ASN         struct {
		Number int64  `json:"autonomous_system_number"`
		Org    string `json:"autonomous_system_organization"`
	} `json:"asn"`
	Geolocation struct {
		Country json.RawMessage `json:"country"`
	} `json:"geolocation"`
}

// proxyPort accepts both numeric and quoted ports; the vakhov list stores the
// port as a JSON string.
type proxyPort int

func (p *proxyPort) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if raw == "" || raw == "null" {
		*p = 0
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		*p = 0
		return nil
	}
	*p = proxyPort(value)
	return nil
}

func flagEnabled(raw json.RawMessage) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(string(raw)), `"`)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

func parseProxiesJSON(data []byte, refreshedAt time.Time) ([]model.Node, error) {
	entries := make([]proxyEntry, 0, 256)
	if data[0] == '{' {
		var wrapper struct {
			Proxies []proxyEntry `json:"proxies"`
			Data    []proxyEntry `json:"data"`
			List    []proxyEntry `json:"list"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return nil, fmt.Errorf("decode proxy JSON: %w", err)
		}
		entries = append(entries, wrapper.Proxies...)
		entries = append(entries, wrapper.Data...)
		entries = append(entries, wrapper.List...)
		if len(entries) == 0 {
			var single proxyEntry
			if err := json.Unmarshal(data, &single); err != nil {
				return nil, fmt.Errorf("decode proxy JSON: %w", err)
			}
			entries = append(entries, single)
		}
	} else if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("decode proxy JSON: %w", err)
	}
	unique := make(map[string]model.Node)
	for _, entry := range entries {
		node, ok := entry.node(refreshedAt)
		if !ok {
			continue
		}
		if current, exists := unique[node.IP]; !exists || node.PingMS < current.PingMS {
			unique[node.IP] = node
		}
	}
	if len(unique) == 0 {
		return nil, errors.New("proxy JSON contains no supported proxies")
	}
	result := make([]model.Node, 0, len(unique))
	for _, node := range unique {
		result = append(result, node)
	}
	return result, nil
}

func (entry proxyEntry) node(refreshedAt time.Time) (model.Node, bool) {
	host := strings.TrimSpace(entry.IP)
	if ip := net.ParseIP(host); ip == nil || ip.To4() == nil {
		host = strings.TrimSpace(entry.Host)
	}
	port := int(entry.Port)
	protocol := ""
	if mapped, ok := proxyProtocol(entry.Protocol); ok {
		protocol = mapped
	}
	if raw := strings.TrimSpace(entry.Proxy); raw != "" {
		if parsed, err := url.Parse(raw); err == nil {
			if host == "" {
				host = parsed.Hostname()
			}
			if port == 0 {
				if parsedPort, err := strconv.Atoi(parsed.Port()); err == nil {
					port = parsedPort
				}
			}
			if protocol == "" {
				if mapped, ok := proxyProtocol(parsed.Scheme); ok {
					protocol = mapped
				}
			}
		}
	}
	if protocol == "" {
		switch {
		case flagEnabled(entry.Socks5):
			protocol = model.ProtocolSOCKS5
		case flagEnabled(entry.HTTP):
			protocol = model.ProtocolHTTP
		}
	}
	if protocol == "" {
		return model.Node{}, false
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return model.Node{}, false
	}
	if port < 1 || port > 65535 {
		return model.Node{}, false
	}
	timeoutMS := int64(entry.Timeout * 1000)
	if entry.Delay > 0 {
		timeoutMS = entry.Delay
	}
	if timeoutMS < 0 {
		timeoutMS = 0
	}
	score := int64(1000) - timeoutMS
	if score < 0 {
		score = 0
	}
	country, countryName := entry.country()
	if country == "" && len(strings.TrimSpace(entry.CountryCode)) == 2 {
		country = strings.ToUpper(strings.TrimSpace(entry.CountryCode))
	}
	if countryName == "" {
		countryName = strings.TrimSpace(entry.CountryName)
	}
	node := model.Node{
		HostName:     fmt.Sprintf("%s://%s:%d", protocol, host, port),
		IP:           host,
		Port:         port,
		Score:        score,
		PingMS:       timeoutMS,
		CountryShort: country,
		CountryLong:  countryName,
		Protocols:    []string{protocol},
		RefreshedAt:  refreshedAt,
	}
	if entry.ASN.Number > 0 {
		node.ASN = fmt.Sprintf("AS%d", entry.ASN.Number)
	}
	node.ASNOrg = strings.TrimSpace(entry.ASN.Org)
	return node, true
}

func (entry proxyEntry) country() (string, string) {
	raw := bytes.TrimSpace(entry.Geolocation.Country)
	if len(raw) == 0 {
		return "", ""
	}
	if raw[0] == '"' {
		var code string
		if err := json.Unmarshal(raw, &code); err != nil {
			return "", ""
		}
		return strings.ToUpper(strings.TrimSpace(code)), ""
	}
	var object struct {
		ISO   string `json:"iso_code"`
		Names struct {
			En string `json:"en"`
		} `json:"names"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", ""
	}
	return strings.ToUpper(strings.TrimSpace(object.ISO)), strings.TrimSpace(object.Names.En)
}

// ParseProxyList accepts protocol://host:port, bare host:port, and comma
// separated host,port. The URL filename decides the default protocol for bare
// entries; authentication is skipped because proxy sessions are anonymous.
func ParseProxyList(data, sourceURL string, refreshedAt time.Time) ([]model.Node, error) {
	fallback := ""
	name := strings.ToLower(sourceURL)
	if index := strings.LastIndex(name, "/"); index >= 0 {
		name = name[index+1:]
	}
	switch {
	case strings.Contains(name, "socks5"):
		fallback = model.ProtocolSOCKS5
	case strings.Contains(name, "http"):
		fallback = model.ProtocolHTTP
	}
	unique := make(map[string]model.Node)
	for _, raw := range strings.Split(data, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		protocol := fallback
		endpoint := line
		if index := strings.Index(endpoint, "://"); index > 0 {
			mapped, ok := proxyProtocol(endpoint[:index])
			if !ok {
				continue
			}
			protocol = mapped
			endpoint = endpoint[index+3:]
		}
		if protocol == "" {
			continue
		}
		if slash := strings.IndexByte(endpoint, '/'); slash >= 0 {
			endpoint = endpoint[:slash]
		}
		if strings.Contains(endpoint, "@") {
			continue
		}
		if fields := strings.Fields(endpoint); len(fields) > 0 {
			endpoint = fields[0]
		}
		if !strings.Contains(endpoint, ":") && strings.Contains(endpoint, ",") {
			endpoint = strings.Replace(endpoint, ",", ":", 1)
		}
		host, portText, err := net.SplitHostPort(endpoint)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil || ip.To4() == nil {
			continue
		}
		if _, exists := unique[host]; exists {
			continue
		}
		unique[host] = model.Node{
			HostName:    fmt.Sprintf("%s://%s:%d", protocol, host, port),
			IP:          host,
			Port:        port,
			Protocols:   []string{protocol},
			RefreshedAt: refreshedAt,
		}
	}
	if len(unique) == 0 {
		return nil, errors.New("proxy list contains no supported proxies")
	}
	result := make([]model.Node, 0, len(unique))
	for _, node := range unique {
		result = append(result, node)
	}
	return result, nil
}

func proxyProtocol(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "socks5", "socks5h":
		return model.ProtocolSOCKS5, true
	case "http", "https", "http_connect":
		return model.ProtocolHTTP, true
	}
	return "", false
}
