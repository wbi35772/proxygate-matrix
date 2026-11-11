package model

import "time"

const (
	ProtocolOpenVPNUDP   = "openvpn_udp"
	ProtocolOpenVPNTCP   = "openvpn_tcp"
	ProtocolSoftEtherTLS = "softether_https"
	ProtocolSSTP         = "sstp"
	ProtocolL2TP         = "l2tp"
	ProtocolSOCKS5       = "socks5"
	ProtocolHTTP         = "http"
)

type Node struct {
	HostName        string    `json:"hostName"`
	IP              string    `json:"ip"`
	Port            int       `json:"port"`
	Score           int64     `json:"score"`
	PingMS          int64     `json:"pingMs"`
	SpeedBPS        int64     `json:"speedBps"`
	CountryLong     string    `json:"countryLong"`
	CountryShort    string    `json:"countryShort"`
	Sessions        int64     `json:"sessions"`
	UptimeMS        int64     `json:"uptimeMs"`
	TotalUsers      int64     `json:"totalUsers"`
	TotalTraffic    int64     `json:"totalTraffic"`
	LogType         string    `json:"logType"`
	Operator        string    `json:"operator"`
	Message         string    `json:"message"`
	OpenVPNConfig   string    `json:"-"`
	Protocols       []string  `json:"protocols"`
	ASN             string    `json:"asn,omitempty"`
	ASNOrg          string    `json:"asnOrg,omitempty"`
	Residential     bool      `json:"residential"`
	FailCount       int       `json:"failCount"`
	QualityScore    float64   `json:"qualityScore"`
	CooldownUntil   time.Time `json:"cooldownUntil,omitzero"`
	SuccessCount    int       `json:"successCount"`
	LastSuccess     time.Time `json:"lastSuccess,omitzero"`
	Source          string    `json:"source,omitempty"`
	MeasuredBPS     int64     `json:"measuredBps"`
	MeasuredAt      time.Time `json:"measuredAt,omitzero"`
	SpeedTestFailed bool      `json:"speedTestFailed"`
	LastFailure     time.Time `json:"lastFailure,omitzero"`
	FailureReason   string    `json:"failureReason,omitempty"`
	FirstSeen       time.Time `json:"firstSeen,omitzero"`
	LastSeen        time.Time `json:"lastSeen,omitzero"`
	RefreshedAt     time.Time `json:"refreshedAt"`
}

type RuntimeStatus struct {
	State            string    `json:"state"`
	ConnectingIP     string    `json:"connectingIp,omitempty"`
	Selection        string    `json:"selection"`
	ActiveIP         string    `json:"activeIp,omitempty"`
	ActiveHostName   string    `json:"activeHostName,omitempty"`
	ActiveProtocol   string    `json:"activeProtocol,omitempty"`
	LastRefresh      time.Time `json:"lastRefresh,omitzero"`
	LastHealthCheck  time.Time `json:"lastHealthCheck,omitzero"`
	LastError        string    `json:"lastError,omitempty"`
	RefreshRunning   bool      `json:"refreshRunning"`
	DirectMode       bool      `json:"directMode"`
	ConnectionPaused bool      `json:"connectionPaused"`
}
