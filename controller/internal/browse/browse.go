package browse

import (
	"context"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	defaultService = "_wattkeeper._tcp"
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

	mu    sync.RWMutex
	nodes map[string]LiveNode
}

func New(logger *log.Logger) *Browser {
	return &Browser{logger: logger, resolver: zeroconfResolver{}, nodes: map[string]LiveNode{}}
}

func (b *Browser) Start(ctx context.Context) error {
	entries := make(chan *zeroconf.ServiceEntry)
	if err := b.resolver.Browse(ctx, defaultService, defaultDomain, entries); err != nil {
		return err
	}
	go b.consume(ctx, entries)
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
