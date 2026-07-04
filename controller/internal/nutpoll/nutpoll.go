package nutpoll

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Foehammer82/wattkeeper/controller/internal/registry"
	"github.com/Foehammer82/wattkeeper/controller/internal/securestore"
)

const defaultNUTPort = 3493

const defaultOfflineThreshold = 3

type client interface {
	Poll(ctx context.Context, address, username, password string) ([]registry.UPSSnapshot, error)
}

type Client struct {
	DialContext func(context.Context, string, string) (net.Conn, error)
}

func NewClient() *Client {
	return &Client{DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext}
}

func (c *Client) Poll(ctx context.Context, address, username, password string) ([]registry.UPSSnapshot, error) {
	if strings.TrimSpace(address) == "" {
		return nil, fmt.Errorf("address is required")
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("NUT credentials are required")
	}
	dial := c.DialContext
	if dial == nil {
		dial = (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	}
	conn, err := dial(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(defaultNUTPort)))
	if err != nil {
		return nil, fmt.Errorf("dial NUT server: %w", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	if err := sendCommand(writer, reader, "USERNAME "+username); err != nil {
		return nil, err
	}
	if err := sendCommand(writer, reader, "PASSWORD "+password); err != nil {
		return nil, err
	}
	upsNames, err := listUPSes(writer, reader)
	if err != nil {
		return nil, err
	}

	snapshots := make([]registry.UPSSnapshot, 0, len(upsNames))
	for _, name := range upsNames {
		variables, err := listVariables(writer, reader, name)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, registry.UPSSnapshot{
			Name:      name,
			Driver:    firstNonEmpty(variables["driver.name"], variables["device.driver"], variables["driver.parameter.driver"]),
			Variables: variables,
		})
	}
	return snapshots, nil
}

type Poller struct {
	Logger           *log.Logger
	Store            *registry.Store
	Vault            *securestore.Store
	Client           client
	Interval         time.Duration
	Retention        time.Duration
	OfflineThreshold int
	OnCycleComplete  func(context.Context) error
	Now              func() time.Time
}

func (p *Poller) Run(ctx context.Context) error {
	interval := p.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if err := p.PollOnce(ctx); err != nil && p.Logger != nil {
		p.Logger.Printf("initial NUT poll failed: %v", err)
	}
	if p.OnCycleComplete != nil {
		if err := p.OnCycleComplete(ctx); err != nil && p.Logger != nil {
			p.Logger.Printf("post-poll hook failed: %v", err)
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := p.PollOnce(ctx); err != nil && p.Logger != nil {
				p.Logger.Printf("NUT poll failed: %v", err)
			}
			if p.OnCycleComplete != nil {
				if err := p.OnCycleComplete(ctx); err != nil && p.Logger != nil {
					p.Logger.Printf("post-poll hook failed: %v", err)
				}
			}
		}
	}
}

func (p *Poller) PollOnce(ctx context.Context) error {
	if p.Store == nil {
		return fmt.Errorf("registry store is required")
	}
	if p.Vault == nil {
		return fmt.Errorf("secure store is required")
	}
	pollClient := p.Client
	if pollClient == nil {
		pollClient = NewClient()
	}
	offlineThreshold := p.OfflineThreshold
	if offlineThreshold <= 0 {
		offlineThreshold = defaultOfflineThreshold
	}
	now := time.Now().UTC()
	if p.Now != nil {
		now = p.Now().UTC()
	}
	nodes, err := p.Store.ListAdoptedNodes(ctx)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if strings.TrimSpace(node.Address) == "" {
			continue
		}
		trust, err := p.Store.LoadNodeTrust(ctx, node.ID)
		if err != nil {
			if p.Logger != nil {
				p.Logger.Printf("load node trust failed node=%s: %v", node.ID, err)
			}
			continue
		}
		password, err := p.Vault.OpenString(trust.NUTPasswordEnc)
		if err != nil {
			_ = p.Store.UpdateNodePollState(ctx, node.ID, nextFailedPollState(node, now, fmt.Errorf("open NUT password: %w", err), offlineThreshold))
			if p.Logger != nil {
				p.Logger.Printf("open NUT password failed node=%s: %v", node.ID, err)
			}
			continue
		}
		snapshots, err := pollClient.Poll(ctx, node.Address, trust.NUTUser, password)
		if err != nil {
			_ = p.Store.UpdateNodePollState(ctx, node.ID, nextFailedPollState(node, now, err, offlineThreshold))
			if p.Logger != nil {
				p.Logger.Printf("poll NUT failed node=%s address=%s: %v", node.ID, node.Address, err)
			}
			continue
		}
		if err := p.Store.UpdateNodePollState(ctx, node.ID, registry.PollState{CommsState: registry.CommsStateHealthy, PollFailures: 0, LastPolledAt: now, LastPollError: ""}); err != nil && p.Logger != nil {
			p.Logger.Printf("update healthy poll state failed node=%s: %v", node.ID, err)
		}
		if err := p.Store.RecordUPSSnapshots(ctx, node.ID, now, snapshots); err != nil {
			if p.Logger != nil {
				p.Logger.Printf("record NUT samples failed node=%s: %v", node.ID, err)
			}
		}
	}
	retention := p.Retention
	if retention > 0 {
		if err := p.Store.PruneSamplesBefore(ctx, now.Add(-retention)); err != nil && p.Logger != nil {
			p.Logger.Printf("prune NUT samples failed: %v", err)
		}
	}
	return nil
}

func sendCommand(writer *bufio.Writer, reader *bufio.Reader, command string) error {
	if _, err := writer.WriteString(command + "\n"); err != nil {
		return fmt.Errorf("write NUT command %q: %w", command, err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush NUT command %q: %w", command, err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read NUT response for %q: %w", command, err)
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "OK") {
		return fmt.Errorf("NUT command %q rejected: %s", command, line)
	}
	return nil
}

func listUPSes(writer *bufio.Writer, reader *bufio.Reader) ([]string, error) {
	if _, err := writer.WriteString("LIST UPS\n"); err != nil {
		return nil, fmt.Errorf("write LIST UPS: %w", err)
	}
	if err := writer.Flush(); err != nil {
		return nil, fmt.Errorf("flush LIST UPS: %w", err)
	}
	start, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read LIST UPS start: %w", err)
	}
	if strings.TrimSpace(start) != "BEGIN LIST UPS" {
		return nil, fmt.Errorf("unexpected LIST UPS start: %s", strings.TrimSpace(start))
	}
	var names []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read LIST UPS body: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "END LIST UPS" {
			break
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) < 2 || parts[0] != "UPS" {
			return nil, fmt.Errorf("unexpected LIST UPS line: %s", line)
		}
		names = append(names, parts[1])
	}
	sort.Strings(names)
	return names, nil
}

func listVariables(writer *bufio.Writer, reader *bufio.Reader, upsName string) (map[string]string, error) {
	if _, err := writer.WriteString("LIST VAR " + upsName + "\n"); err != nil {
		return nil, fmt.Errorf("write LIST VAR %s: %w", upsName, err)
	}
	if err := writer.Flush(); err != nil {
		return nil, fmt.Errorf("flush LIST VAR %s: %w", upsName, err)
	}
	start, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read LIST VAR start for %s: %w", upsName, err)
	}
	if strings.TrimSpace(start) != "BEGIN LIST VAR "+upsName {
		return nil, fmt.Errorf("unexpected LIST VAR start for %s: %s", upsName, strings.TrimSpace(start))
	}
	variables := map[string]string{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read LIST VAR body for %s: %w", upsName, err)
		}
		line = strings.TrimSpace(line)
		if line == "END LIST VAR "+upsName {
			break
		}
		parts := strings.SplitN(line, " ", 4)
		if len(parts) < 4 || parts[0] != "VAR" || parts[1] != upsName {
			return nil, fmt.Errorf("unexpected LIST VAR line for %s: %s", upsName, line)
		}
		value, err := strconv.Unquote(parts[3])
		if err != nil {
			value = strings.Trim(parts[3], `"`)
		}
		variables[parts[2]] = value
	}
	return variables, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nextFailedPollState(node registry.Node, now time.Time, pollErr error, offlineThreshold int) registry.PollState {
	failures := node.PollFailures + 1
	state := registry.CommsStateDegraded
	if failures >= offlineThreshold {
		state = registry.CommsStateOffline
	}
	message := "poll failed"
	if pollErr != nil {
		message = strings.TrimSpace(pollErr.Error())
	}
	return registry.PollState{
		CommsState:    state,
		PollFailures:  failures,
		LastPolledAt:  now,
		LastPollError: message,
	}
}
