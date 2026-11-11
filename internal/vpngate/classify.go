package vpngate

import (
	"regexp"
	"strings"

	"github.com/ExpTechTW/proxygate/internal/model"
)

var asnPattern = regexp.MustCompile(`(?i)^(AS\d+)\s+(.+)$`)

// Hosting and cloud providers are not residential exits. A VPN Gate endpoint
// hosted by one of these networks is kept in the pool but ranked last and can
// be excluded with matrix.residentialOnly.
var hostingKeywords = []string{
	"amazon", "aws", "google", "microsoft", "azure", "digitalocean", "linode",
	"vultr", "choopa", "datacamp", "m247", "leaseweb", "contabo", "scaleway",
	"oracle", "alibaba", "tencent", "gcore", "fastly", "cloudflare", "akamai",
	"psychz", "quadranet", "colocrossing", "interserver", "hostinger",
	"namecheap", "godaddy", "ionos", "kamatera", "upcloud", "cherryservers",
	"zenlayer", "cogent", "hosting", "datacenter", "data center", "colocation",
	"server", " vps", "cloud", "dedicated",
}

// Consumer and incumbent carriers are the preferred residential exit networks.
var carrierKeywords = []string{
	"ntt", "kddi", "softbank", "okinawa", "biglobe", "plala", "so-net", "@nifty",
	"docomo", "au one", "korea telecom", "kt corp", "sk broadband", "sk telecom",
	"lg u+", "hanaro", "china telecom", "china unicom", "chunghwa", "hinet",
	"singtel", "telekom", "telefonica", "orange", "vodafone", "comcast",
	"verizon", "at&t", "charter", "cox", "shaw", "rogers", "telstra",
}

func hasVPNProtocol(protocols []string) bool {
	for _, protocol := range protocols {
		switch protocol {
		case model.ProtocolOpenVPNUDP, model.ProtocolOpenVPNTCP, model.ProtocolSoftEtherTLS, model.ProtocolSSTP, model.ProtocolL2TP:
			return true
		}
	}
	return false
}

// Classify extracts the ASN and tags an endpoint as residential when its
// operator looks like a consumer or carrier network. Unknown operators are
// treated as residential because VPN Gate itself is a volunteer network, and
// a hosting keyword still demotes them.
func Classify(node *model.Node) {
	operator := strings.TrimSpace(node.Operator)
	if node.ASNOrg == "" || strings.EqualFold(node.ASNOrg, operator) {
		node.ASNOrg = operator
	}
	if node.ASN == "" {
		if match := asnPattern.FindStringSubmatch(operator); match != nil {
			node.ASN = strings.ToUpper(match[1])
			node.ASNOrg = strings.TrimSpace(match[2])
		}
	}
	ClassifyResidential(node)
}

// ClassifyResidential tags a node from its operator label and resolved network
// name. Unknown networks are treated as residential because VPN Gate nodes are
// volunteer endpoints, while a hosting keyword still demotes them.
func ClassifyResidential(node *model.Node) {
	lower := strings.ToLower(strings.TrimSpace(node.Operator + " " + node.ASNOrg))
	residential := true
	for _, keyword := range carrierKeywords {
		if strings.Contains(lower, keyword) {
			residential = true
			break
		}
	}
	for _, keyword := range hostingKeywords {
		if strings.Contains(lower, keyword) {
			residential = false
			break
		}
	}
	node.Residential = residential
}
