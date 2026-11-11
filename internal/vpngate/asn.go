package vpngate

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/model"
)

type asnInfo struct {
	ASN     string
	Org     string
	Country string
}

type asnCacheEntry struct {
	info asnInfo
	at   time.Time
}

type asnCall struct {
	done chan struct{}
	info asnInfo
}

// ASNResolver enriches nodes with the network that actually announces their
// address. VPN Gate's Operator column is a volunteer display name, so it
// cannot be used as an ISP/ASN tag. Team Cymru's DNS service is free, needs no
// key, and answers both origin ASN and AS name queries.
type ASNResolver struct {
	mu       sync.Mutex
	cache    map[string]asnCacheEntry
	inflight map[string]*asnCall
	ttl      time.Duration
	negative time.Duration
	timeout  time.Duration
	lookup   func(context.Context, string) ([]string, error)
}

func NewASNResolver() *ASNResolver {
	return &ASNResolver{
		cache:    make(map[string]asnCacheEntry),
		inflight: make(map[string]*asnCall),
		ttl:      24 * time.Hour,
		negative: time.Hour,
		timeout:  2 * time.Second,
		lookup:   net.DefaultResolver.LookupTXT,
	}
}

func (r *ASNResolver) Lookup(ctx context.Context, ip string) asnInfo {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() == nil {
		return asnInfo{}
	}
	r.mu.Lock()
	if entry, ok := r.cache[ip]; ok {
		age := time.Since(entry.at)
		if (entry.info.ASN != "" && age < r.ttl) || (entry.info.ASN == "" && age < r.negative) {
			r.mu.Unlock()
			return entry.info
		}
	}
	if call, ok := r.inflight[ip]; ok {
		r.mu.Unlock()
		select {
		case <-call.done:
			return call.info
		case <-ctx.Done():
			return asnInfo{}
		}
	}
	call := &asnCall{done: make(chan struct{})}
	r.inflight[ip] = call
	r.mu.Unlock()

	info := r.resolve(ctx, parsed.To4())
	call.info = info
	r.mu.Lock()
	r.cache[ip] = asnCacheEntry{info: info, at: time.Now()}
	delete(r.inflight, ip)
	close(call.done)
	r.mu.Unlock()
	return info
}

func (r *ASNResolver) resolve(ctx context.Context, ip net.IP) asnInfo {
	lookupCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	reversed := strings.Join([]string{
		strconv.Itoa(int(ip[3])), strconv.Itoa(int(ip[2])),
		strconv.Itoa(int(ip[1])), strconv.Itoa(int(ip[0])),
	}, ".")
	records, err := r.lookup(lookupCtx, reversed+".origin.asn.cymru.com")
	if err != nil || len(records) == 0 {
		return asnInfo{}
	}
	fields := strings.Split(records[0], "|")
	if len(fields) < 3 {
		return asnInfo{}
	}
	asn := strings.ToUpper(strings.TrimSpace(fields[0]))
	if !strings.HasPrefix(asn, "AS") {
		asn = "AS" + asn
	}
	if asn == "AS" || strings.Trim(asn[2:], "0123456789") != "" {
		return asnInfo{}
	}
	info := asnInfo{ASN: asn}
	if len(fields) > 2 {
		info.Country = strings.TrimSpace(fields[2])
	}
	orgCtx, orgCancel := context.WithTimeout(ctx, r.timeout)
	defer orgCancel()
	orgRecords, orgErr := r.lookup(orgCtx, strings.ToLower(asn)+".asn.cymru.com")
	if orgErr == nil && len(orgRecords) > 0 {
		orgFields := strings.Split(orgRecords[0], "|")
		if len(orgFields) >= 5 {
			info.Org = strings.TrimSpace(orgFields[len(orgFields)-1])
		}
	}
	if info.Org == "" {
		info.Org = asn
	}
	return info
}

// Enrich fills ASN and ASNOrg for nodes that do not have them yet. It is
// bounded so a slow resolver cannot stall a refresh indefinitely.
func (r *ASNResolver) Enrich(ctx context.Context, nodes []model.Node, workers int) int {
	if workers < 1 {
		workers = 1
	}
	if workers > 32 {
		workers = 32
	}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		enriched int
	)
	queue := make(chan int)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range queue {
				node := &nodes[index]
				info := r.Lookup(ctx, node.IP)
				if info.ASN == "" {
					continue
				}
				node.ASN = info.ASN
				node.ASNOrg = info.Org
				if node.CountryShort == "" && len(info.Country) == 2 {
					node.CountryShort = strings.ToUpper(info.Country)
				}
				ClassifyResidential(node)
				mu.Lock()
				enriched++
				mu.Unlock()
			}
		}()
	}
	for index := range nodes {
		if ctx.Err() != nil {
			break
		}
		if nodes[index].ASN != "" && nodes[index].ASNOrg != "" && !strings.EqualFold(nodes[index].ASNOrg, nodes[index].Operator) {
			continue
		}
		queue <- index
	}
	close(queue)
	wg.Wait()
	return enriched
}
