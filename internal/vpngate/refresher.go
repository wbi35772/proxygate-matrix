package vpngate

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/model"
	"github.com/ExpTechTW/proxygate/internal/store"
)

type Refresher struct {
	config   *config.Store
	store    *store.Store
	client   *Client
	logger   *log.Logger
	trigger  chan struct{}
	mu       sync.Mutex
	stateMu  sync.RWMutex
	running  bool
	lastErr  error
	activeIP func() string
	after    func(context.Context)
	asn      *ASNResolver
}

func NewRefresher(configStore *config.Store, database *store.Store, logger *log.Logger, activeIP func() string, after func(context.Context)) *Refresher {
	return &Refresher{config: configStore, store: database, client: NewClient(), logger: logger, trigger: make(chan struct{}, 1), activeIP: activeIP, after: after, asn: NewASNResolver()}
}

func (r *Refresher) Trigger() bool {
	select {
	case r.trigger <- struct{}{}:
		return true
	default:
		return false
	}
}

func (r *Refresher) Running() bool {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.running
}

func (r *Refresher) LastError() error {
	r.stateMu.RLock()
	defer r.stateMu.RUnlock()
	return r.lastErr
}

func (r *Refresher) Run(ctx context.Context) {
	for {
		settings := r.config.Get()
		interval, _ := settings.RefreshDuration()
		if settings.Matrix.Enabled {
			if matrixInterval, err := settings.Matrix.HarvestDuration(); err == nil {
				interval = matrixInterval
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-r.trigger:
			timer.Stop()
		case <-timer.C:
		}
		if err := r.Refresh(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Printf("[refresh] %v", err)
		}
	}
}

func (r *Refresher) sources(settings config.Config) []string {
	if settings.Matrix.Enabled && len(settings.Matrix.Sources) > 0 {
		return settings.Matrix.Sources
	}
	return []string{settings.SourceURL}
}

func (r *Refresher) Refresh(ctx context.Context) error {
	if !r.mu.TryLock() {
		return errors.New("node refresh is already running")
	}
	defer r.mu.Unlock()
	r.setState(true, nil)
	settings := r.config.Get()
	refreshedAt := time.Now()
	merged := make(map[string]model.Node)
	var failures []error
	successes := 0
	for _, source := range r.sources(settings) {
		if ctx.Err() != nil {
			r.setState(false, ctx.Err())
			return ctx.Err()
		}
		nodes, err := r.client.Fetch(ctx, source, settings.FilterExpression)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", source, err))
			r.logger.Printf("[refresh] source=%s error=%v", source, err)
			continue
		}
		successes++
		for index := range nodes {
			nodes[index].RefreshedAt = refreshedAt
			nodes[index].LastSeen = refreshedAt
			nodes[index].Source = source
			Classify(&nodes[index])
			current, exists := merged[nodes[index].IP]
			if !exists || nodes[index].Score > current.Score {
				merged[nodes[index].IP] = nodes[index]
			}
		}
		r.logger.Printf("[refresh] source=%s fetched=%d", source, len(nodes))
	}
	if successes == 0 {
		err := fmt.Errorf("all node sources failed: %w", errors.Join(failures...))
		r.setState(false, err)
		return err
	}
	nodes := make([]model.Node, 0, len(merged))
	for _, node := range merged {
		nodes = append(nodes, node)
	}
	if settings.Matrix.Enabled && r.asn != nil {
		enriched := r.asn.Enrich(ctx, nodes, 8)
		r.logger.Printf("[refresh] ASN enrichment=%d", enriched)
	}
	residential := 0
	for _, node := range nodes {
		if node.Residential {
			residential++
		}
	}
	preserveIP := ""
	if r.activeIP != nil {
		preserveIP = r.activeIP()
	}
	legacy := nodes
	if settings.Matrix.Enabled {
		legacy = make([]model.Node, 0, len(nodes))
		for _, node := range nodes {
			if node.Residential && hasVPNProtocol(node.Protocols) {
				legacy = append(legacy, node)
			}
		}
	}
	if err := r.store.UpsertNodes(ctx, legacy, refreshedAt, preserveIP); err != nil {
		r.setState(false, err)
		return err
	}
	if err := r.store.UpsertPoolNodes(ctx, nodes, "harvest", refreshedAt); err != nil {
		r.setState(false, err)
		return err
	}
	if settings.Matrix.Enabled {
		if ttl, err := settings.Matrix.NodeTTLDuration(); err == nil {
			if removed, pruneErr := r.store.PrunePoolNodes(ctx, refreshedAt.Add(-ttl)); pruneErr != nil {
				r.logger.Printf("[refresh] prune pool: %v", pruneErr)
			} else if removed > 0 {
				r.logger.Printf("[refresh] pruned %d expired pool rows", removed)
			}
		}
	}
	r.logger.Printf("[refresh] stored nodes=%d residential=%d sources=%d", len(nodes), residential, successes)
	if r.after != nil {
		r.after(ctx)
	}
	var finalErr error
	if len(failures) > 0 {
		finalErr = errors.Join(failures...)
		r.logger.Printf("[refresh] partial source failure: %v", finalErr)
	}
	r.setState(false, finalErr)
	return finalErr
}

func (r *Refresher) setState(running bool, err error) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.running = running
	r.lastErr = err
}
