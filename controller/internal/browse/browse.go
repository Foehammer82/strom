package browse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	defaultService = "_strom._tcp"
	defaultDomain  = "local."
)

type resolver interface {
	Browse(ctx context.Context, service, domain string, entries chan<- *zeroconf.ServiceEntry) error
}

type zeroconfResolver struct{}

func (zeroconfResolver) Browse(ctx context.Context, service, domain string, entries chan<- *zeroconf.ServiceEntry) error {
	res, err := zeroconf.NewResolver(nil)
	if err != nil {
		return err
	}
	return res.Browse(ctx, service, domain, entries)
}

type LiveNode struct {
	ID       string    `json:"id"`
	Instance string    `json:"instance"`
	Hostname string    `json:"hostname"`
	Address  string    `json:"address"`
	Port     int       `json:"port"`
	Version  string    `json:"version"`
	UPSCount int       `json:"ups_count"`
	Adopted  bool      `json:"adopted"`
	LastSeen time.Time `json:"last_seen"`
}

type Browser struct {
	logger   *log.Logger
	resolver resolver
	client   *http.Client
	seeds    []seedTarget

	mu    sync.RWMutex
	nodes map[string]LiveNode
}

type seedTarget struct {
	host string
	port int
}

type seedHealthResponse struct {
	Version string `json:"version"`
	Serial  string `json:"serial"`
	UPSes   []any  `json:"upses"`
}

func New(logger *log.Logger) *Browser {
	return &Browser{
		logger:   logger,
		resolver: zeroconfResolver{},
		client:   &http.Client{Timeout: 3 * time.Second},
		nodes:    map[string]LiveNode{},
	}
}

func (b *Browser) ConfigureSeeds(raw string) {
	b.seeds = parseSeedTargets(raw)
}

func (b *Browser) Start(ctx context.Context) error {
	entries := make(chan *zeroconf.ServiceEntry)
	if err := b.resolver.Browse(ctx, defaultService, defaultDomain, entries); err != nil {
		if len(b.seeds) == 0 {
			return err
		}
		if b.logger != nil {
			b.logger.Printf("mDNS browse unavailable, using discovery seeds only: %v", err)
		}
	} else {
		go b.consume(ctx, entries)
	}

	if len(b.seeds) > 0 {
		go b.seedLoop(ctx)
	}
	return nil
}

func (b *Browser) consume(ctx context.Context, entries <-chan *zeroconf.ServiceEntry) {
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-entries:
			if !ok {
				return
			}
			node, ok := parseEntry(entry)
			if !ok {
				continue
			}
			b.mu.Lock()
			b.nodes[node.ID] = node
			b.mu.Unlock()
		}
	}
}

func (b *Browser) Snapshot() []LiveNode {
	b.mu.RLock()
	defer b.mu.RUnlock()
	nodes := make([]LiveNode, 0, len(b.nodes))
	for _, node := range b.nodes {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	return nodes
}

func parseEntry(entry *zeroconf.ServiceEntry) (LiveNode, bool) {
	if entry == nil {
		return LiveNode{}, false
	}
	meta := parseTXT(entry.Text)
	id := strings.TrimSpace(meta["id"])
	if id == "" {
		return LiveNode{}, false
	}
	upsCount, _ := strconv.Atoi(meta["ups_count"])
	adopted, _ := strconv.ParseBool(meta["adopted"])
	return LiveNode{
		ID:       id,
		Instance: entry.Instance,
		Hostname: entry.HostName,
		Address:  firstAddress(entry),
		Port:     entry.Port,
		Version:  meta["version"],
		UPSCount: upsCount,
		Adopted:  adopted,
		LastSeen: time.Now().UTC(),
	}, true
}

func parseTXT(records []string) map[string]string {
	meta := make(map[string]string, len(records))
	for _, record := range records {
		key, value, ok := strings.Cut(record, "=")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return meta
}

func firstAddress(entry *zeroconf.ServiceEntry) string {
	for _, address := range entry.AddrIPv4 {
		if ip := normalizeIP(address); ip != "" {
			return ip
		}
	}
	for _, address := range entry.AddrIPv6 {
		if ip := normalizeIP(address); ip != "" {
			return ip
		}
	}
	return ""
}

func normalizeIP(address net.IP) string {
	if address == nil {
		return ""
	}
	return address.String()
}

func parseSeedTargets(raw string) []seedTarget {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	targets := make([]seedTarget, 0, len(parts))
	for _, part := range parts {
		text := strings.TrimSpace(part)
		if text == "" {
			continue
		}
		host, portText, ok := strings.Cut(text, ":")
		if !ok {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(portText))
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		targets = append(targets, seedTarget{host: host, port: port})
	}
	return targets
}

func (b *Browser) seedLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	b.updateSeedNodes(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			b.updateSeedNodes(ctx)
		}
	}
}

func (b *Browser) updateSeedNodes(ctx context.Context) {
	for _, target := range b.seeds {
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", target.host)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			ipText := strings.TrimSpace(ip.String())
			if ipText == "" {
				continue
			}
			if parsed, parseErr := netip.ParseAddr(ipText); parseErr == nil && parsed.IsLoopback() {
				continue
			}
			node, ok := b.fetchSeedNode(ctx, target, ipText)
			if !ok {
				continue
			}
			b.mu.Lock()
			b.nodes[node.ID] = node
			b.mu.Unlock()
		}
	}
}

func (b *Browser) fetchSeedNode(ctx context.Context, target seedTarget, address string) (LiveNode, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s:%d/healthz", address, target.port), nil)
	if err != nil {
		return LiveNode{}, false
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return LiveNode{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return LiveNode{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return LiveNode{}, false
	}
	var health seedHealthResponse
	if err := json.Unmarshal(body, &health); err != nil {
		return LiveNode{}, false
	}
	serial := strings.TrimSpace(health.Serial)
	if serial == "" {
		return LiveNode{}, false
	}
	return LiveNode{
		ID:       serial,
		Instance: "seed-" + target.host,
		Hostname: target.host,
		Address:  address,
		Port:     target.port,
		Version:  strings.TrimSpace(health.Version),
		UPSCount: len(health.UPSes),
		Adopted:  false,
		LastSeen: time.Now().UTC(),
	}, true
}
