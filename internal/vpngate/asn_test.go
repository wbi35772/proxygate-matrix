package vpngate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ExpTechTW/proxygate/internal/model"
)

func TestASNResolverLookupAndCache(t *testing.T) {
	resolver := NewASNResolver()
	calls := 0
	resolver.lookup = func(_ context.Context, name string) ([]string, error) {
		calls++
		switch {
		case strings.HasSuffix(name, ".origin.asn.cymru.com"):
			return []string{"2516 | 59.138.0.0/16 | JP | apnic | 2004-10-19"}, nil
		case strings.HasPrefix(name, "as2516."):
			return []string{"2516 | JP | apnic | 2002-04-05 | KDDI - KDDI CORPORATION, JP"}, nil
		}
		return nil, fmt.Errorf("unexpected query %s", name)
	}

	info := resolver.Lookup(context.Background(), "59.138.34.86")
	if info.ASN != "AS2516" || info.Org != "KDDI - KDDI CORPORATION, JP" || info.Country != "JP" {
		t.Fatalf("unexpected info: %+v", info)
	}
	if calls != 2 {
		t.Fatalf("expected 2 DNS queries, got %d", calls)
	}
	_ = resolver.Lookup(context.Background(), "59.138.34.86")
	if calls != 2 {
		t.Fatalf("cache miss repeated DNS queries: %d", calls)
	}
}

func TestASNResolverRejectsInvalidIP(t *testing.T) {
	resolver := NewASNResolver()
	if info := resolver.Lookup(context.Background(), "not-an-ip"); info.ASN != "" {
		t.Fatalf("expected empty info, got %+v", info)
	}
}

func TestEnrichSetsNetworkAndResidential(t *testing.T) {
	resolver := NewASNResolver()
	resolver.lookup = func(_ context.Context, name string) ([]string, error) {
		switch {
		case strings.HasSuffix(name, ".origin.asn.cymru.com"):
			return []string{"16509 | 13.0.0.0/8 | US | arin | 2002-04-05"}, nil
		case strings.HasPrefix(name, "as16509."):
			return []string{"16509 | US | arin | 2002-04-05 | AMAZON-02, US"}, nil
		}
		return nil, fmt.Errorf("unexpected query %s", name)
	}
	nodes := []model.Node{
		{IP: "13.1.2.3", Operator: "volunteer node"},
		{IP: "59.138.34.86", Operator: "volunteer node"},
	}
	resolver.cache["59.138.34.86"] = asnCacheEntry{info: asnInfo{ASN: "AS2516", Org: "KDDI CORPORATION", Country: "JP"}, at: time.Now()}
	for index := range nodes {
		Classify(&nodes[index])
	}
	enriched := resolver.Enrich(context.Background(), nodes, 2)
	if enriched != 2 {
		t.Fatalf("expected 2 enriched nodes, got %d", enriched)
	}
	if nodes[0].ASN != "AS16509" || nodes[0].Residential {
		t.Fatalf("hosting node not tagged: %+v", nodes[0])
	}
	if nodes[1].ASN != "AS2516" || !nodes[1].Residential {
		t.Fatalf("carrier node not tagged: %+v", nodes[1])
	}
}
