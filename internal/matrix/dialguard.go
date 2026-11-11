package matrix

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/model"
)

var errDialBudget = errors.New("dial guard: candidate skipped")

const (
	maxDialWait   = 15 * time.Second
	portHardStop  = 20
	guardPause    = 10 * time.Minute
	maxCandidates = 12
)

// dialGuard keeps pool verification from looking like a port scan. Every
// attempt to reach a pool candidate passes through Acquire, which enforces a
// steady dial rate, a bounded number of distinct targets per destination port
// and a global hourly target budget. Failed targets cool down so retries never
// re-sweep the same addresses.
type dialGuard struct {
	config *config.Store

	mu          sync.Mutex
	tokens      float64
	lastRefill  time.Time
	windowStart time.Time
	hourStart   time.Time
	perPort     map[int]map[string]struct{}
	global      map[string]struct{}
	cooling     map[string]time.Time
	pausedUntil time.Time
	pauseReason string
	allowed     int64
	blocked     int64
	skipped     int64
}

func newDialGuard(configStore *config.Store) *dialGuard {
	return &dialGuard{
		config:  configStore,
		perPort: make(map[int]map[string]struct{}),
		global:  make(map[string]struct{}),
		cooling: make(map[string]time.Time),
	}
}

// Acquire reserves one outbound attempt to ip:port. It waits for a token when
// the dial rate is saturated and reports errDialBudget when the candidate must
// be skipped instead of dialed.
func (g *dialGuard) Acquire(ctx context.Context, ip string, port int) error {
	settings := g.config.Get().Matrix
	if settings.DialRate <= 0 || ip == "" {
		return nil
	}
	for {
		now := time.Now()
		g.mu.Lock()
		if !g.pausedUntil.IsZero() && now.Before(g.pausedUntil) {
			reason := g.pauseReason
			g.mu.Unlock()
			return fmt.Errorf("%w: paused: %s", errDialBudget, reason)
		}
		if g.lastRefill.IsZero() {
			g.tokens = float64(max(settings.DialBurst, 1))
			g.lastRefill = now
		}
		g.tokens += now.Sub(g.lastRefill).Seconds() * settings.DialRate
		if burst := float64(max(settings.DialBurst, 1)); g.tokens > burst {
			g.tokens = burst
		}
		g.lastRefill = now
		if g.tokens < 1 {
			wait := time.Duration((1 - g.tokens) / settings.DialRate * float64(time.Second))
			g.mu.Unlock()
			if wait > maxDialWait {
				g.countBlocked()
				return errDialBudget
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if g.windowStart.IsZero() || now.Sub(g.windowStart) >= time.Minute {
			g.windowStart = now
			g.perPort = make(map[int]map[string]struct{})
		}
		if g.hourStart.IsZero() || now.Sub(g.hourStart) >= time.Hour {
			g.hourStart = now
			g.global = make(map[string]struct{})
		}
		set := g.perPort[port]
		if set == nil {
			set = make(map[string]struct{})
			g.perPort[port] = set
		}
		_, seenPort := set[ip]
		_, seenGlobal := g.global[ip]
		if !seenPort && settings.PortIPPerMinute > 0 && len(set) >= settings.PortIPPerMinute {
			g.blocked++
			g.mu.Unlock()
			return errDialBudget
		}
		if !seenGlobal && settings.GlobalIPPerHour > 0 && len(g.global) >= settings.GlobalIPPerHour {
			g.blocked++
			g.mu.Unlock()
			return errDialBudget
		}
		g.tokens--
		set[ip] = struct{}{}
		g.global[ip] = struct{}{}
		g.allowed++
		portTargets := len(set)
		g.mu.Unlock()
		if portTargets > portHardStop && settings.PortIPPerMinute > portHardStop {
			g.pause(fmt.Sprintf("port %d saw %d distinct targets in one minute", port, portTargets))
		}
		if delay := jitterDelay(settings.DialMinMS, settings.DialMaxMS); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		return nil
	}
}

// jitterDelay returns a random pause inside [minMS, maxMS]. A fixed cadence is
// what makes pool verification look scripted, so every allowed dial waits a
// different amount.
func jitterDelay(minMS, maxMS int) time.Duration {
	if minMS <= 0 && maxMS <= 0 {
		return 0
	}
	if minMS < 0 {
		minMS = 0
	}
	if maxMS < minMS {
		maxMS = minMS
	}
	span := maxMS - minMS
	if span <= 0 {
		return time.Duration(minMS) * time.Millisecond
	}
	return time.Duration(minMS+rand.IntN(span+1)) * time.Millisecond
}

func (g *dialGuard) pause(reason string) {
	g.mu.Lock()
	g.pausedUntil = time.Now().Add(guardPause)
	g.pauseReason = reason
	g.mu.Unlock()
}

func (g *dialGuard) countBlocked() {
	g.mu.Lock()
	g.blocked++
	g.mu.Unlock()
}

// MarkFailure puts a target into a cooldown window so later switches do not
// dial the same address again.
func (g *dialGuard) MarkFailure(ip string) {
	if ip == "" {
		return
	}
	settings := g.config.Get().Matrix
	if settings.FailCooldownMinutes <= 0 {
		return
	}
	g.mu.Lock()
	g.cooling[ip] = time.Now().Add(time.Duration(settings.FailCooldownMinutes) * time.Minute)
	g.mu.Unlock()
}

func (g *dialGuard) MarkSuccess(ip string) {
	if ip == "" {
		return
	}
	g.mu.Lock()
	delete(g.cooling, ip)
	g.mu.Unlock()
}

func (g *dialGuard) isCooling(ip string) bool {
	if ip == "" {
		return false
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	until, exists := g.cooling[ip]
	if !exists {
		return false
	}
	if now.After(until) {
		delete(g.cooling, ip)
		return false
	}
	return true
}

// FilterCandidates removes targets that must not be dialed: blocked ports,
// cooling addresses, repeated ports and anything past the candidate budget.
// exemptIP is the slot's previous node, which may be retried on purpose.
func (g *dialGuard) FilterCandidates(nodes []model.Node, settings config.Config, exemptIP string) []model.Node {
	if len(nodes) == 0 {
		return nodes
	}
	blocked := make(map[int]struct{}, len(settings.Matrix.BlockedPorts))
	for _, port := range settings.Matrix.BlockedPorts {
		blocked[port] = struct{}{}
	}
	perPort := make(map[int]int)
	result := make([]model.Node, 0, len(nodes))
	for _, node := range nodes {
		port := candidatePort(node)
		if _, bad := blocked[port]; bad {
			g.countSkipped()
			continue
		}
		if node.IP != exemptIP && g.isCooling(node.IP) {
			g.countSkipped()
			continue
		}
		if limit := settings.Matrix.SamePortCandidates; limit > 0 && perPort[port] >= limit {
			g.countSkipped()
			continue
		}
		perPort[port]++
		result = append(result, node)
		if len(result) >= maxCandidates {
			break
		}
	}
	return result
}

func (g *dialGuard) countSkipped() {
	g.mu.Lock()
	g.skipped++
	g.mu.Unlock()
}

// DialStats is the observability snapshot exposed at /api/matrix/dialstats.
type DialStats struct {
	Allowed         int64          `json:"allowed"`
	Blocked         int64          `json:"blocked"`
	Skipped         int64          `json:"skipped"`
	Cooling         int            `json:"cooling"`
	Ports           map[string]int `json:"portsThisMinute"`
	TargetsThisHour int            `json:"targetsThisHour"`
	Paused          bool           `json:"paused"`
	PauseReason     string         `json:"pauseReason,omitempty"`
	PausedUntil     time.Time      `json:"pausedUntil,omitzero"`
	DialRate        float64        `json:"dialRate"`
	DialBurst       int            `json:"dialBurst"`
	DialMinMS       int            `json:"dialMinMs"`
	DialMaxMS       int            `json:"dialMaxMs"`
	PortIPPerMinute int            `json:"portIpPerMinute"`
	GlobalIPPerHour int            `json:"globalIpPerHour"`
	CoolingMinutes  int            `json:"failCooldownMinutes"`
	BlockedPorts    []int          `json:"blockedPorts"`
}

func (g *dialGuard) Snapshot() DialStats {
	settings := g.config.Get().Matrix
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	ports := make(map[string]int, len(g.perPort))
	if !g.windowStart.IsZero() && now.Sub(g.windowStart) < time.Minute {
		for port, set := range g.perPort {
			ports[strconv.Itoa(port)] = len(set)
		}
	}
	targets := 0
	if !g.hourStart.IsZero() && now.Sub(g.hourStart) < time.Hour {
		targets = len(g.global)
	}
	cooling := 0
	for ip, until := range g.cooling {
		if now.After(until) {
			delete(g.cooling, ip)
			continue
		}
		cooling++
	}
	paused := !g.pausedUntil.IsZero() && now.Before(g.pausedUntil)
	return DialStats{
		Allowed:         g.allowed,
		Blocked:         g.blocked,
		Skipped:         g.skipped,
		Cooling:         cooling,
		Ports:           ports,
		TargetsThisHour: targets,
		Paused:          paused,
		PauseReason:     g.pauseReason,
		PausedUntil:     g.pausedUntil,
		DialRate:        settings.DialRate,
		DialBurst:       settings.DialBurst,
		DialMinMS:       settings.DialMinMS,
		DialMaxMS:       settings.DialMaxMS,
		PortIPPerMinute: settings.PortIPPerMinute,
		GlobalIPPerHour: settings.GlobalIPPerHour,
		CoolingMinutes:  settings.FailCooldownMinutes,
		BlockedPorts:    settings.BlockedPorts,
	}
}
