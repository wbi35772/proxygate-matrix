package config

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ExpTechTW/proxygate/internal/model"
)

const (
	DefaultSourceURL    = "http://www.vpngate.net/api/iphone/"
	DefaultSpeedTestURL = "http://storage.googleapis.com/gcp-public-data-landsat/LC08/01/001/002/LC08_L1GT_001002_20160817_20170322_01_T2/LC08_L1GT_001002_20160817_20170322_01_T2_B1.TIF"
)

var defaultDNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

var legacyProtocolPriority = []string{model.ProtocolOpenVPNUDP, model.ProtocolOpenVPNTCP, model.ProtocolSoftEtherTLS, model.ProtocolSSTP, model.ProtocolL2TP}

var legacyDefaultSources = []string{"https://www.vpngate.net/api/iphone/", "http://www.vpngate.net/api/iphone/"}

// DefaultProtocolPriority keeps the VPN Gate protocols first and adds the
// public SOCKS5/HTTP proxy transports used by the extra node sources.
func DefaultProtocolPriority() []string {
	return []string{
		model.ProtocolOpenVPNUDP, model.ProtocolOpenVPNTCP, model.ProtocolSoftEtherTLS,
		model.ProtocolSSTP, model.ProtocolL2TP, model.ProtocolSOCKS5, model.ProtocolHTTP,
	}
}

// DefaultMatrixSources fuses VPN Gate with community proxy lists that publish
// country and ASN metadata. Plain host:port lists are still accepted by the
// parser when added manually.
func DefaultMatrixSources() []string {
	return []string{
		"https://www.vpngate.net/api/iphone/",
		"http://www.vpngate.net/api/iphone/",
		"https://raw.githubusercontent.com/monosans/proxy-list/main/proxies.json",
		"https://raw.githubusercontent.com/vakhov/fresh-proxy-list/master/proxylist.json",
		"https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/protocols/socks5/data.json",
		"https://cdn.jsdelivr.net/gh/proxifly/free-proxy-list@main/proxies/protocols/http/data.json",
	}
}

// migrateLegacyDefaults upgrades configurations written by builds that only
// knew the five VPN Gate protocols, without touching custom orderings.
func migrateLegacyDefaults(value *Config) {
	if slices.Equal(value.ProtocolPriority, legacyProtocolPriority) {
		value.ProtocolPriority = DefaultProtocolPriority()
	}
}

type Config struct {
	SourceURL              string   `json:"sourceUrl"`
	RefreshInterval        string   `json:"refreshInterval"`
	FilterExpression       string   `json:"filterExpression"`
	SelectionMode          string   `json:"selectionMode"`
	FollowRankingOnRefresh bool     `json:"followRankingOnRefresh"`
	FallbackToDirect       bool     `json:"fallbackToDirect"`
	SOCKS5                 SOCKS5   `json:"socks5"`
	Monitor                Monitor  `json:"monitor"`
	Web                    Web      `json:"web"`
	DatabasePath           string   `json:"databasePath"`
	DNSServers             []string `json:"dnsServers"`
	SpeedTestURL           string   `json:"speedTestUrl"`
	SpeedTestTimeout       string   `json:"speedTestTimeout"`
	ProtocolPriority       []string `json:"protocolPriority"`
	ConnectTimeout         string   `json:"connectTimeout"`
	VPNGateUsername        string   `json:"vpnGateUsername"`
	VPNGatePassword        string   `json:"vpnGatePassword"`
	VPNGatePreSharedKey    string   `json:"vpnGatePreSharedKey"`
	Matrix                 Matrix   `json:"matrix"`
}

// Matrix configures the multi-slot residential gateway. The zero value keeps
// the feature disabled so configurations written by older releases remain
// valid; defaults are applied immediately after decoding.
type Matrix struct {
	Enabled                bool     `json:"enabled"`
	Slots                  int      `json:"slots"`
	PortStart              int      `json:"portStart"`
	ListenAddress          string   `json:"listenAddress"`
	PublicHost             string   `json:"publicHost"`
	UsernamePrefix         string   `json:"usernamePrefix"`
	Password               string   `json:"password"`
	APIKey                 string   `json:"apiKey"`
	HarvestInterval        string   `json:"harvestInterval"`
	NodeTTL                string   `json:"nodeTtl"`
	RotateInterval         string   `json:"rotateInterval"`
	Autostart              bool     `json:"autostart"`
	ResidentialOnly        bool     `json:"residentialOnly"`
	PreferredCountries     []string `json:"preferredCountries"`
	PreferredASNs          []string `json:"preferredAsns"`
	Sources                []string `json:"sources"`
	SubscriptionListen     string   `json:"subscriptionListen"`
	SubscriptionToken      string   `json:"subscriptionToken"`
	SubscriptionPath       string   `json:"subscriptionPath"`
	SubscriptionTrojanPath string   `json:"subscriptionTrojanPath"`
	// SubscriptionClients are extra subscription tokens, each with optional
	// profile presets. A client can be revoked by removing it here; its
	// Trojan/VLESS credentials derive from its token and stop working at once.
	SubscriptionClients []SubscriptionClient `json:"subscriptionClients,omitempty"`
	PublicSocksHost     string               `json:"publicSocksHost"`
	FrontProxyEnabled   bool                 `json:"frontProxyEnabled"`
	FrontProxyType      string               `json:"frontProxyType"`
	FrontProxyURL       string               `json:"frontProxyUrl"`
	FrontProxySlot      int                  `json:"frontProxySlot"`
	FrontProxyAPI       string               `json:"frontProxyApi"`
	FrontProxyTimeout   string               `json:"frontProxyTimeout"`
	DialRate            float64              `json:"dialRate"`
	DialBurst           int                  `json:"dialBurst"`
	PortIPPerMinute     int                  `json:"portIpPerMinute"`
	GlobalIPPerHour     int                  `json:"globalIpPerHour"`
	FailCooldownMinutes int                  `json:"failCooldownMinutes"`
	StartStaggerMS      int                  `json:"startStaggerMs"`
	SamePortCandidates  int                  `json:"samePortCandidates"`
	// ShareLimit caps how many ready nodes subscriptions, the share pool and
	// the local access bundle expose. 0 shares every ready node.
	ShareLimit int `json:"shareLimit"`
	// DialMinMS and DialMaxMS bound the randomized pause before each pool
	// dial, so verification never runs at a fixed cadence.
	DialMinMS int   `json:"dialMinMs"`
	DialMaxMS int   `json:"dialMaxMs"`
	BlockedPorts []int `json:"blockedPorts"`
}

// SubscriptionClient is a named subscription token with optional defaults.
// Query parameters on the subscription URL override every preset.
type SubscriptionClient struct {
	Name      string   `json:"name"`
	Token     string   `json:"token"`
	Count     int      `json:"count,omitempty"`
	Countries []string `json:"countries,omitempty"`
	Exclude   []string `json:"exclude,omitempty"`
	Shuffle   bool     `json:"shuffle,omitempty"`
	Group     string   `json:"group,omitempty"`
	Sort      string   `json:"sort,omitempty"`
}

func (m *Matrix) applyDefaults() {
	if m.Slots == 0 {
		m.Slots = 35
	}
	if m.PortStart == 0 {
		m.PortStart = 10801
	}
	if m.ListenAddress == "" {
		m.ListenAddress = "0.0.0.0"
	}
	if m.UsernamePrefix == "" {
		m.UsernamePrefix = "slot_"
	}
	if m.HarvestInterval == "" {
		m.HarvestInterval = "3m"
	}
	if m.NodeTTL == "" {
		m.NodeTTL = "6h"
	}
	if m.RotateInterval == "" {
		m.RotateInterval = "5m"
	}
	if len(m.PreferredCountries) == 0 {
		m.PreferredCountries = []string{"JP", "KR"}
	}
	if len(m.PreferredASNs) == 0 {
		m.PreferredASNs = []string{"NTT", "KDDI", "SOFTBANK", "KT", "SK BROADBAND", "LG U+"}
	}
	if len(m.Sources) == 0 || slices.Equal(m.Sources, legacyDefaultSources) {
		m.Sources = DefaultMatrixSources()
	}
	if m.SubscriptionListen == "" {
		m.SubscriptionListen = ":8090"
	}
	if m.SubscriptionPath == "" {
		m.SubscriptionPath = "/pgws"
	}
	if m.SubscriptionTrojanPath == "" {
		m.SubscriptionTrojanPath = "/pgtrojan"
	}
	if m.FrontProxyType == "" {
		m.FrontProxyType = "socks5"
	}
	if m.FrontProxyTimeout == "" {
		m.FrontProxyTimeout = "15s"
	}
	if m.DialRate == 0 {
		m.DialRate = 1
	}
	if m.DialBurst <= 0 {
		m.DialBurst = 3
	}
	if m.PortIPPerMinute == 0 {
		m.PortIPPerMinute = 10
	}
	if m.GlobalIPPerHour == 0 {
		m.GlobalIPPerHour = 100
	}
	if m.FailCooldownMinutes == 0 {
		m.FailCooldownMinutes = 360
	}
	if m.StartStaggerMS == 0 {
		m.StartStaggerMS = 3000
	}
	if m.SamePortCandidates == 0 {
		m.SamePortCandidates = 2
	}
	if m.ShareLimit < 0 {
		m.ShareLimit = 0
	}
	if m.DialMinMS == 0 {
		m.DialMinMS = 900
	}
	if m.DialMaxMS == 0 {
		m.DialMaxMS = 1500
	}
	if m.DialMaxMS < m.DialMinMS {
		m.DialMaxMS = m.DialMinMS
	}
	if len(m.BlockedPorts) == 0 {
		m.BlockedPorts = []int{999, 6666, 31337}
	}
}

// SubscriptionTokenValue returns the secret that guards subscription URLs. When
// no explicit token is configured it is derived from the API key so enabling
// the subscription never ships a well-known default.
func (m Matrix) SubscriptionTokenValue() string {
	if m.SubscriptionToken != "" {
		return m.SubscriptionToken
	}
	sum := sha256.Sum256([]byte(m.APIKey))
	return hex.EncodeToString(sum[:12])
}

// MatchSubscriptionToken resolves a subscription path token to the effective
// token used for node credentials and the matching client preset, if any.
func (m Matrix) MatchSubscriptionToken(token string) (string, *SubscriptionClient) {
	if token == "" {
		return "", nil
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(m.SubscriptionTokenValue())) == 1 {
		return m.SubscriptionTokenValue(), nil
	}
	for index := range m.SubscriptionClients {
		client := &m.SubscriptionClients[index]
		if subtle.ConstantTimeCompare([]byte(token), []byte(client.Token)) == 1 {
			return client.Token, client
		}
	}
	return "", nil
}

func (m Matrix) HarvestDuration() (time.Duration, error) {
	return positiveDuration(m.HarvestInterval)
}

func (m Matrix) NodeTTLDuration() (time.Duration, error) {
	return positiveDuration(m.NodeTTL)
}

func (m Matrix) RotateIntervalDuration() (time.Duration, error) {
	return positiveDuration(m.RotateInterval)
}

func (m Matrix) Username(slotID int) string {
	return fmt.Sprintf("%s%02d", m.UsernamePrefix, slotID)
}

func (m Matrix) SlotAddress(slotID int) string {
	return net.JoinHostPort(m.ListenAddress, strconv.Itoa(m.PortStart+slotID-1))
}

type SOCKS5 struct {
	ListenAddress    string `json:"listenAddress"`
	Username         string `json:"username"`
	Password         string `json:"password"`
	IdleTimeout      string `json:"idleTimeout"`
	HalfCloseTimeout string `json:"halfCloseTimeout"`
}

func (s SOCKS5) IdleTimeoutDuration() (time.Duration, error) {
	if s.IdleTimeout == "" {
		return 5 * time.Minute, nil
	}
	return positiveDuration(s.IdleTimeout)
}

func (s SOCKS5) HalfCloseTimeoutDuration() (time.Duration, error) {
	if s.HalfCloseTimeout == "" {
		return time.Minute, nil
	}
	return positiveDuration(s.HalfCloseTimeout)
}

type Monitor struct {
	URL      string `json:"url"`
	Interval string `json:"interval"`
	Timeout  string `json:"timeout"`
}

type Web struct {
	ListenAddress string `json:"listenAddress"`
	Username      string `json:"username"`
	PasswordHash  string `json:"passwordHash"`
	SessionSecret string `json:"sessionSecret"`
}

func Default() Config {
	value, _ := defaultConfig()
	return value
}

func defaultConfig() (Config, string) {
	password := randomPassword()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Sprintf("generate initial web password hash: %v", err))
	}
	return Config{
		SourceURL: DefaultSourceURL, RefreshInterval: "30m", FilterExpression: "true",
		SelectionMode: "speed", FollowRankingOnRefresh: true, FallbackToDirect: false,
		SOCKS5:       SOCKS5{ListenAddress: "127.0.0.1:1080", IdleTimeout: "5m", HalfCloseTimeout: "1m"},
		Monitor:      Monitor{URL: "https://www.google.com/generate_204", Interval: "30s", Timeout: "10s"},
		Web:          Web{ListenAddress: "127.0.0.1:8080", Username: "admin", PasswordHash: string(hash), SessionSecret: randomSecret()},
		DatabasePath: "data/proxygate.db", ConnectTimeout: "15s",
		DNSServers: append([]string(nil), defaultDNSServers...), SpeedTestURL: DefaultSpeedTestURL, SpeedTestTimeout: "45s",
		ProtocolPriority: DefaultProtocolPriority(),
		VPNGateUsername:  "vpn", VPNGatePassword: "vpn", VPNGatePreSharedKey: "vpn",
		Matrix: Matrix{},
	}, password
}

func (c Config) Validate() error {
	if parsed, err := url.ParseRequestURI(c.SourceURL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("sourceUrl must be an HTTP or HTTPS URL")
	}
	if _, err := c.RefreshDuration(); err != nil {
		return fmt.Errorf("refreshInterval: %w", err)
	}
	if c.SelectionMode != "speed" && c.SelectionMode != "ping" && c.SelectionMode != "score" {
		return errors.New("selectionMode must be speed, ping, or score")
	}
	if err := validateAddress("socks5.listenAddress", c.SOCKS5.ListenAddress); err != nil {
		return err
	}
	if (c.SOCKS5.Username == "") != (c.SOCKS5.Password == "") {
		return errors.New("socks5 username and password must both be set or both be empty")
	}
	if len(c.SOCKS5.Username) > 255 || len(c.SOCKS5.Password) > 255 {
		return errors.New("socks5 username and password must not exceed 255 bytes")
	}
	if _, err := c.SOCKS5.IdleTimeoutDuration(); err != nil {
		return fmt.Errorf("socks5.idleTimeout: %w", err)
	}
	if _, err := c.SOCKS5.HalfCloseTimeoutDuration(); err != nil {
		return fmt.Errorf("socks5.halfCloseTimeout: %w", err)
	}
	if err := validateAddress("web.listenAddress", c.Web.ListenAddress); err != nil {
		return err
	}
	if c.Web.Username == "" || c.Web.PasswordHash == "" || c.Web.SessionSecret == "" {
		return errors.New("web username, passwordHash, and sessionSecret are required")
	}
	if parsed, err := url.ParseRequestURI(c.Monitor.URL); err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("monitor.url must be an HTTP or HTTPS URL")
	}
	if _, err := c.MonitorInterval(); err != nil {
		return fmt.Errorf("monitor.interval: %w", err)
	}
	if _, err := c.MonitorTimeout(); err != nil {
		return fmt.Errorf("monitor.timeout: %w", err)
	}
	if _, err := c.ConnectTimeoutDuration(); err != nil {
		return fmt.Errorf("connectTimeout: %w", err)
	}
	if strings.TrimSpace(c.DatabasePath) == "" {
		return errors.New("databasePath is required")
	}
	if len(c.DNSServers) == 0 {
		return errors.New("dnsServers must not be empty")
	}
	seenDNS := make(map[string]bool, len(c.DNSServers))
	for _, server := range c.DNSServers {
		host, portText, err := net.SplitHostPort(server)
		if err != nil {
			return fmt.Errorf("dnsServers contains invalid address %q: %w", server, err)
		}
		if ip := net.ParseIP(host); ip == nil || ip.To4() == nil {
			return fmt.Errorf("dnsServers entry %q must use a literal IPv4 address", server)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("dnsServers entry %q has an invalid port", server)
		}
		if seenDNS[server] {
			return fmt.Errorf("dnsServers contains duplicate address %q", server)
		}
		seenDNS[server] = true
	}
	if parsed, err := url.ParseRequestURI(c.SpeedTestURL); err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("speedTestUrl must be an HTTP or HTTPS URL")
	}
	if _, err := c.SpeedTestTimeoutDuration(); err != nil {
		return fmt.Errorf("speedTestTimeout: %w", err)
	}
	validProtocols := []string{model.ProtocolSoftEtherTLS, model.ProtocolOpenVPNTCP, model.ProtocolOpenVPNUDP, model.ProtocolSSTP, model.ProtocolL2TP, model.ProtocolSOCKS5, model.ProtocolHTTP}
	if len(c.ProtocolPriority) == 0 {
		return errors.New("protocolPriority must not be empty")
	}
	seen := make(map[string]bool)
	for _, protocol := range c.ProtocolPriority {
		if !slices.Contains(validProtocols, protocol) || seen[protocol] {
			return fmt.Errorf("invalid or duplicate protocol %q", protocol)
		}
		seen[protocol] = true
	}
	if c.VPNGateUsername == "" || c.VPNGatePassword == "" || c.VPNGatePreSharedKey == "" {
		return errors.New("VPN Gate username, password, and pre-shared key are required")
	}
	if strings.ContainsAny(c.VPNGateUsername, "\r\n") || strings.ContainsAny(c.VPNGatePassword, "\r\n") {
		return errors.New("VPN Gate username and password must not contain newlines")
	}
	return c.validateMatrix()
}

func (c Config) validateMatrix() error {
	m := c.Matrix
	if !m.Enabled {
		return nil
	}
	if m.Slots < 1 || m.Slots > 256 {
		return errors.New("matrix.slots must be between 1 and 256")
	}
	if m.PortStart < 1 || m.PortStart+m.Slots-1 > 65535 {
		return errors.New("matrix.portStart and matrix.slots exceed the valid TCP port range")
	}
	if m.ListenAddress == "" || strings.ContainsAny(m.ListenAddress, " \t\r\n/") {
		return errors.New("matrix.listenAddress is invalid")
	}
	if m.UsernamePrefix == "" || strings.ContainsAny(m.UsernamePrefix, ":\r\n") || len(m.UsernamePrefix) > 200 {
		return errors.New("matrix.usernamePrefix is invalid")
	}
	if m.Password == "" || len(m.Password) > 255 || strings.ContainsAny(m.Password, "\r\n") {
		return errors.New("matrix.password must be between 1 and 255 bytes and must not contain newlines")
	}
	if m.APIKey == "" {
		return errors.New("matrix.apiKey is required when the matrix is enabled")
	}
	if _, err := m.HarvestDuration(); err != nil {
		return fmt.Errorf("matrix.harvestInterval: %w", err)
	}
	if _, err := m.NodeTTLDuration(); err != nil {
		return fmt.Errorf("matrix.nodeTtl: %w", err)
	}
	if _, err := m.RotateIntervalDuration(); err != nil {
		return fmt.Errorf("matrix.rotateInterval: %w", err)
	}
	for _, source := range m.Sources {
		parsed, err := url.ParseRequestURI(source)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("matrix.sources contains an invalid URL %q", source)
		}
	}
	for _, country := range m.PreferredCountries {
		if len(country) != 2 {
			return fmt.Errorf("matrix.preferredCountries contains invalid country code %q", country)
		}
	}
	if m.ShareLimit < 0 || m.ShareLimit > 256 {
		return errors.New("matrix.shareLimit must be between 0 and 256")
	}
	if m.DialMinMS < 0 || m.DialMaxMS < 0 || m.DialMinMS > 30000 || m.DialMaxMS > 30000 {
		return errors.New("matrix.dialMinMs and matrix.dialMaxMs must be between 0 and 30000")
	}
	if m.DialMaxMS > 0 && m.DialMaxMS < m.DialMinMS {
		return errors.New("matrix.dialMaxMs must not be smaller than matrix.dialMinMs")
	}
	if err := m.validateSubscriptionClients(); err != nil {
		return err
	}
	return nil
}

func (m Matrix) validateSubscriptionClients() error {
	if len(m.SubscriptionClients) > 64 {
		return errors.New("matrix.subscriptionClients supports at most 64 entries")
	}
	names := make(map[string]bool, len(m.SubscriptionClients))
	tokens := map[string]bool{m.SubscriptionTokenValue(): true}
	for index, client := range m.SubscriptionClients {
		name := strings.TrimSpace(client.Name)
		if name == "" || len(name) > 64 || strings.ContainsAny(name, "\r\n") {
			return fmt.Errorf("matrix.subscriptionClients[%d].name must be 1-64 characters without newlines", index)
		}
		if names[name] {
			return fmt.Errorf("matrix.subscriptionClients contains duplicate name %q", name)
		}
		names[name] = true
		token := strings.TrimSpace(client.Token)
		if !validSubscriptionToken(token) {
			return fmt.Errorf("matrix.subscriptionClients[%d].token must be 8-128 characters from [A-Za-z0-9._~-]", index)
		}
		if tokens[token] {
			return fmt.Errorf("matrix.subscriptionClients contains duplicate token at index %d", index)
		}
		tokens[token] = true
		if client.Count < 0 || client.Count > 256 {
			return fmt.Errorf("matrix.subscriptionClients[%d].count must be between 0 and 256", index)
		}
		for _, list := range [][]string{client.Countries, client.Exclude} {
			for _, code := range list {
				code = strings.TrimSpace(code)
				if len(code) != 2 {
					return fmt.Errorf("matrix.subscriptionClients[%d] contains invalid country code %q", index, code)
				}
			}
		}
		switch strings.ToLower(strings.TrimSpace(client.Group)) {
		case "", "country", "none":
		default:
			return fmt.Errorf("matrix.subscriptionClients[%d].group must be country or none", index)
		}
		switch strings.ToLower(strings.TrimSpace(client.Sort)) {
		case "", "slot", "country", "name":
		default:
			return fmt.Errorf("matrix.subscriptionClients[%d].sort must be slot, country, or name", index)
		}
	}
	return nil
}

func validSubscriptionToken(token string) bool {
	if len(token) < 8 || len(token) > 128 {
		return false
	}
	for _, symbol := range token {
		switch {
		case symbol >= 'a' && symbol <= 'z', symbol >= 'A' && symbol <= 'Z', symbol >= '0' && symbol <= '9':
		case symbol == '.', symbol == '_', symbol == '~', symbol == '-':
		default:
			return false
		}
	}
	return true
}

func positiveDuration(value string) (time.Duration, error) {
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, errors.New("must be greater than zero")
	}
	return duration, nil
}

func (c Config) RefreshDuration() (time.Duration, error) { return positiveDuration(c.RefreshInterval) }
func (c Config) MonitorInterval() (time.Duration, error) { return positiveDuration(c.Monitor.Interval) }
func (c Config) MonitorTimeout() (time.Duration, error)  { return positiveDuration(c.Monitor.Timeout) }
func (c Config) SpeedTestTimeoutDuration() (time.Duration, error) {
	return positiveDuration(c.SpeedTestTimeout)
}
func (c Config) ConnectTimeoutDuration() (time.Duration, error) {
	return positiveDuration(c.ConnectTimeout)
}

func validateAddress(field, value string) error {
	if _, _, err := net.SplitHostPort(value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
}

type Store struct {
	mu     sync.RWMutex
	path   string
	config Config
}

func Load(path string) (*Store, error) {
	return load(path, nil)
}

// LoadWithLogger behaves like Load and reports the generated first-run
// password exactly once, when a configuration file is created.
func LoadWithLogger(path string, logger *log.Logger) (*Store, error) {
	return load(path, logger)
}

func load(path string, logger *log.Logger) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	var value Config
	initialPassword := ""
	data, readErr := os.ReadFile(absolute)
	if readErr == nil {
		if err := requireCurrentConfigFields(data); err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, errors.New("decode config: trailing JSON data")
		}
	} else if errors.Is(readErr, os.ErrNotExist) {
		value, initialPassword = defaultConfig()
	} else {
		return nil, fmt.Errorf("read config: %w", readErr)
	}
	value.Matrix.applyDefaults()
	migrateLegacyDefaults(&value)
	applyEnvironmentOverrides(&value)
	if !filepath.IsAbs(value.DatabasePath) {
		value.DatabasePath = filepath.Join(filepath.Dir(absolute), value.DatabasePath)
	}
	if err := value.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	store := &Store{path: absolute, config: value}
	if errors.Is(readErr, os.ErrNotExist) {
		if err := store.Save(value); err != nil {
			return nil, err
		}
		if logger != nil {
			logger.Printf("[config] generated first-run web password for %s: %s", value.Web.Username, initialPassword)
		}
	}
	return store, nil
}

func applyEnvironmentOverrides(value *Config) {
	if address := strings.TrimSpace(os.Getenv("PROXYGATE_WEB_LISTEN_ADDRESS")); address != "" {
		value.Web.ListenAddress = address
	}
	if address := strings.TrimSpace(os.Getenv("PROXYGATE_SOCKS5_LISTEN_ADDRESS")); address != "" {
		value.SOCKS5.ListenAddress = address
	}
	if path := strings.TrimSpace(os.Getenv("PROXYGATE_DATABASE_PATH")); path != "" {
		value.DatabasePath = path
	}
	if raw := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_ENABLED")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			value.Matrix.Enabled = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_AUTOSTART")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			value.Matrix.Autostart = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_RESIDENTIAL_ONLY")); raw != "" {
		if parsed, err := strconv.ParseBool(raw); err == nil {
			value.Matrix.ResidentialOnly = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_SLOTS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			value.Matrix.Slots = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_PORT_START")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			value.Matrix.PortStart = parsed
		}
	}
	if address := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_LISTEN_ADDRESS")); address != "" {
		value.Matrix.ListenAddress = address
	}
	if host := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_PUBLIC_HOST")); host != "" {
		value.Matrix.PublicHost = host
	}
	if password := os.Getenv("PROXYGATE_MATRIX_PASSWORD"); password != "" {
		value.Matrix.Password = password
	}
	if key := os.Getenv("PROXYGATE_MATRIX_API_KEY"); key != "" {
		value.Matrix.APIKey = key
	}
	if key := os.Getenv("PROXYGATE_API_KEY"); key != "" {
		value.Matrix.APIKey = key
	}
	if interval := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_HARVEST_INTERVAL")); interval != "" {
		value.Matrix.HarvestInterval = interval
	}
	if ttl := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_NODE_TTL")); ttl != "" {
		value.Matrix.NodeTTL = ttl
	}
	if interval := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_ROTATE_INTERVAL")); interval != "" {
		value.Matrix.RotateInterval = interval
	}
	if listen := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_SUB_LISTEN")); listen != "" {
		switch strings.ToLower(listen) {
		case "off", "false", "disabled", "-":
			value.Matrix.SubscriptionListen = ""
		default:
			value.Matrix.SubscriptionListen = listen
		}
	}
	if token := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_SUB_TOKEN")); token != "" {
		value.Matrix.SubscriptionToken = token
	}
	if path := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_SUB_PATH")); path != "" {
		value.Matrix.SubscriptionPath = path
	}
	if path := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_SUB_TROJAN_PATH")); path != "" {
		value.Matrix.SubscriptionTrojanPath = path
	}
	if host := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_PUBLIC_SOCKS_HOST")); host != "" {
		value.Matrix.PublicSocksHost = host
	}
	if limit := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_SHARE_LIMIT")); limit != "" {
		if parsed, err := strconv.Atoi(limit); err == nil {
			value.Matrix.ShareLimit = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_DIAL_MIN_MS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			value.Matrix.DialMinMS = parsed
		}
	}
	if raw := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_DIAL_MAX_MS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			value.Matrix.DialMaxMS = parsed
		}
	}
	if setting := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_FRONT_PROXY")); setting != "" {
		switch strings.ToLower(setting) {
		case "off", "false", "0", "disabled", "-":
			value.Matrix.FrontProxyEnabled = false
		default:
			value.Matrix.FrontProxyEnabled = true
		}
	}
	if kind := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_FRONT_PROXY_TYPE")); kind != "" {
		value.Matrix.FrontProxyType = kind
	}
	if endpoint := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_FRONT_PROXY_URL")); endpoint != "" {
		value.Matrix.FrontProxyURL = endpoint
	}
	if slot := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_FRONT_PROXY_SLOT")); slot != "" {
		if id, err := strconv.Atoi(slot); err == nil {
			value.Matrix.FrontProxySlot = id
		}
	}
	if endpoint := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_FRONT_PROXY_API")); endpoint != "" {
		value.Matrix.FrontProxyAPI = endpoint
	}
	if timeout := strings.TrimSpace(os.Getenv("PROXYGATE_MATRIX_FRONT_PROXY_TIMEOUT")); timeout != "" {
		value.Matrix.FrontProxyTimeout = timeout
	}
}

func requireCurrentConfigFields(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if err := requireJSONFields(fields, "config",
		"sourceUrl", "refreshInterval", "filterExpression", "selectionMode", "followRankingOnRefresh",
		"socks5", "monitor", "web", "databasePath", "dnsServers", "speedTestUrl", "speedTestTimeout",
		"protocolPriority", "connectTimeout", "vpnGateUsername", "vpnGatePassword", "vpnGatePreSharedKey"); err != nil {
		return err
	}
	for name, required := range map[string][]string{
		"socks5":  {"listenAddress", "username", "password"},
		"monitor": {"url", "interval", "timeout"},
		"web":     {"listenAddress", "username", "passwordHash", "sessionSecret"},
	} {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(fields[name], &nested); err != nil {
			return fmt.Errorf("%s must be an object", name)
		}
		if err := requireJSONFields(nested, name, required...); err != nil {
			return err
		}
	}
	return nil
}

func requireJSONFields(fields map[string]json.RawMessage, object string, required ...string) error {
	for _, name := range required {
		if _, exists := fields[name]; !exists {
			return fmt.Errorf("%s is missing required field %q", object, name)
		}
	}
	return nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.config
	value.ProtocolPriority = append([]string(nil), value.ProtocolPriority...)
	value.DNSServers = append([]string(nil), value.DNSServers...)
	value.Matrix.PreferredCountries = append([]string(nil), value.Matrix.PreferredCountries...)
	value.Matrix.PreferredASNs = append([]string(nil), value.Matrix.PreferredASNs...)
	value.Matrix.Sources = append([]string(nil), value.Matrix.Sources...)
	return value
}

func (s *Store) Save(value Config) error {
	if !filepath.IsAbs(value.DatabasePath) {
		value.DatabasePath = filepath.Join(filepath.Dir(s.path), value.DatabasePath)
	}
	value.Matrix.applyDefaults()
	migrateLegacyDefaults(&value)
	if err := value.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	encodedValue := value
	relative, err := filepath.Rel(filepath.Dir(s.path), value.DatabasePath)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		encodedValue.DatabasePath = relative
	}
	data, err := json.MarshalIndent(encodedValue, "", "  ")
	if err != nil {
		return err
	}
	temporary := s.path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, s.path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	s.mu.Lock()
	s.config = value
	s.mu.Unlock()
	return nil
}

func randomSecret() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

func randomPassword() string {
	return base64.RawURLEncoding.EncodeToString(randomBytes(18))
}

func randomBytes(size int) []byte {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic(fmt.Sprintf("read cryptographic randomness: %v", err))
	}
	return data
}
