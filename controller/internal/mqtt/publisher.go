package mqtt

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

const defaultHeartbeat = 5 * time.Minute

type RuntimeConfig struct {
	BrokerURL       string
	Username        string
	Password        string
	DiscoveryPrefix string
	StatePrefix     string
	ClientID        string
	Heartbeat       time.Duration
	KeepAlive       uint16
	CommandHandler  CommandHandler
}

type NodeSnapshot struct {
	Node  NodeInfo
	UPSes []UPSInfo
}

type CommandRequest struct {
	NodeID  string
	UPSName string
	Command string
}

type CommandHandler func(context.Context, CommandRequest) error

type publishClient interface {
	AwaitConnection(context.Context) error
	Publish(context.Context, *paho.Publish) (*paho.PublishResponse, error)
	Subscribe(context.Context, *paho.Subscribe) (*paho.Suback, error)
	AddOnPublishReceived(func(autopaho.PublishReceived) (bool, error)) func()
}

type publishedState struct {
	payload []byte
	at      time.Time
}

type Publisher struct {
	logger     *log.Logger
	cfg        RuntimeConfig
	client     publishClient
	now        func() time.Time
	handler    CommandHandler
	mu         sync.Mutex
	published  map[string]publishedState
	discovery  map[string]struct{}
	routes     map[string]commandRoute
	subscribed bool
}

type commandRoute struct {
	nodeID   string
	upsName  string
	commands map[string]string
}

func NewPublisher(ctx context.Context, logger *log.Logger, cfg RuntimeConfig) (*Publisher, error) {
	if strings.TrimSpace(cfg.BrokerURL) == "" {
		return nil, nil
	}
	serverURL, err := url.Parse(cfg.BrokerURL)
	if err != nil {
		return nil, fmt.Errorf("parse mqtt broker url: %w", err)
	}
	statePrefix := defaultString(strings.TrimSpace(cfg.StatePrefix), defaultStatePrefix)
	keepAlive := cfg.KeepAlive
	if keepAlive == 0 {
		keepAlive = 20
	}
	clientID := strings.TrimSpace(cfg.ClientID)
	if clientID == "" {
		clientID = "strom-controller"
	}
	cm, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		KeepAlive:                     keepAlive,
		CleanStartOnInitialConnection: false,
		SessionExpiryInterval:         600,
		ConnectUsername:               strings.TrimSpace(cfg.Username),
		ConnectPassword:               []byte(cfg.Password),
		WillMessage: &paho.WillMessage{
			Topic:   statePrefix + "/controller/availability",
			Payload: []byte("offline"),
			QoS:     1,
			Retain:  true,
		},
		ClientConfig: paho.ClientConfig{ClientID: clientID},
	})
	if err != nil {
		return nil, fmt.Errorf("create mqtt connection: %w", err)
	}
	publisher := &Publisher{
		logger: logger,
		cfg: RuntimeConfig{
			BrokerURL: cfg.BrokerURL, Username: cfg.Username, Password: cfg.Password,
			DiscoveryPrefix: defaultString(strings.TrimSpace(cfg.DiscoveryPrefix), defaultDiscoveryPrefix),
			StatePrefix:     defaultString(strings.TrimSpace(cfg.StatePrefix), defaultStatePrefix),
			ClientID:        clientID,
			Heartbeat:       heartbeatOrDefault(cfg.Heartbeat),
			KeepAlive:       keepAlive,
		},
		client:    cm,
		now:       time.Now,
		handler:   cfg.CommandHandler,
		published: map[string]publishedState{},
		discovery: map[string]struct{}{},
		routes:    map[string]commandRoute{},
	}
	publisher.registerReceiver()
	return publisher, nil
}

func NewTestPublisher(cfg RuntimeConfig, client publishClient, now func() time.Time) *Publisher {
	publisher := &Publisher{
		cfg: RuntimeConfig{
			DiscoveryPrefix: defaultString(strings.TrimSpace(cfg.DiscoveryPrefix), defaultDiscoveryPrefix),
			StatePrefix:     defaultString(strings.TrimSpace(cfg.StatePrefix), defaultStatePrefix),
			Heartbeat:       heartbeatOrDefault(cfg.Heartbeat),
		},
		client: client,
		now: func() time.Time {
			if now != nil {
				return now()
			}
			return time.Now()
		},
		handler:   cfg.CommandHandler,
		published: map[string]publishedState{},
		discovery: map[string]struct{}{},
		routes:    map[string]commandRoute{},
	}
	publisher.registerReceiver()
	return publisher
}

func (p *Publisher) PublishSnapshots(ctx context.Context, snapshots []NodeSnapshot) error {
	if p == nil || p.client == nil {
		return nil
	}
	if err := p.client.AwaitConnection(ctx); err != nil {
		return err
	}
	if err := p.ensureCommandSubscription(ctx); err != nil {
		return err
	}
	p.updateRoutes(snapshots)
	if err := p.publish(ctx, PublishMessage{Topic: p.cfg.StatePrefix + "/controller/availability", Payload: []byte("online"), Retain: true}, false); err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		availability, err := AvailabilityMessage(Config{DiscoveryPrefix: p.cfg.DiscoveryPrefix, StatePrefix: p.cfg.StatePrefix}, snapshot.Node)
		if err != nil {
			return err
		}
		if err := p.publish(ctx, availability, false); err != nil {
			return err
		}
		for _, ups := range snapshot.UPSes {
			discoveryMessages, err := DiscoveryMessages(Config{DiscoveryPrefix: p.cfg.DiscoveryPrefix, StatePrefix: p.cfg.StatePrefix}, snapshot.Node, ups)
			if err != nil {
				return err
			}
			for _, message := range discoveryMessages {
				if err := p.publish(ctx, message, true); err != nil {
					return err
				}
			}
			stateMessage, err := StateMessage(Config{DiscoveryPrefix: p.cfg.DiscoveryPrefix, StatePrefix: p.cfg.StatePrefix}, snapshot.Node, ups)
			if err != nil {
				return err
			}
			if err := p.publish(ctx, stateMessage, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *Publisher) ensureCommandSubscription(ctx context.Context) error {
	p.mu.Lock()
	if p.subscribed {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	subscription := &paho.Subscribe{Subscriptions: []paho.SubscribeOptions{{Topic: p.commandSubscriptionTopic(), QoS: 1}}}
	if _, err := p.client.Subscribe(ctx, subscription); err != nil {
		return err
	}
	p.mu.Lock()
	p.subscribed = true
	p.mu.Unlock()
	return nil
}

func (p *Publisher) registerReceiver() {
	if p == nil || p.client == nil || p.handler == nil {
		return
	}
	p.client.AddOnPublishReceived(func(received autopaho.PublishReceived) (bool, error) {
		packet := received.Packet
		if packet == nil {
			return false, nil
		}
		topic := strings.TrimSpace(packet.Topic)
		if !strings.HasPrefix(topic, p.cfg.StatePrefix+"/nodes/") || !strings.HasSuffix(topic, "/command") {
			return false, nil
		}
		command := strings.TrimSpace(string(packet.Payload))
		if command == "" {
			return true, nil
		}
		target, mappedCommand, ok := p.lookupRoute(topic, command)
		if !ok {
			if p.logger != nil {
				p.logger.Printf("mqtt command ignored topic=%s command=%s", topic, command)
			}
			return true, nil
		}
		go func() {
			commandCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err := p.handler(commandCtx, CommandRequest{NodeID: target.nodeID, UPSName: target.upsName, Command: mappedCommand})
			if err != nil {
				if p.logger != nil {
					p.logger.Printf("mqtt command failed node=%s ups=%s command=%s err=%v", target.nodeID, target.upsName, mappedCommand, err)
				}
				return
			}
			if p.logger != nil {
				p.logger.Printf("mqtt command executed node=%s ups=%s command=%s", target.nodeID, target.upsName, mappedCommand)
			}
		}()
		return true, nil
	})
}

func (p *Publisher) commandSubscriptionTopic() string {
	return p.cfg.StatePrefix + "/nodes/+/ups/+/command"
}

func (p *Publisher) updateRoutes(snapshots []NodeSnapshot) {
	routes := make(map[string]commandRoute)
	for _, snapshot := range snapshots {
		nodeID := strings.TrimSpace(snapshot.Node.ID)
		if nodeID == "" {
			continue
		}
		for _, ups := range snapshot.UPSes {
			upsName := strings.TrimSpace(ups.Name)
			if upsName == "" {
				continue
			}
			topic := p.cfg.StatePrefix + "/nodes/" + slug(nodeID) + "/ups/" + slug(upsName) + "/command"
			route := commandRoute{
				nodeID:   nodeID,
				upsName:  upsName,
				commands: map[string]string{},
			}
			for _, command := range ups.Commands {
				trimmed := strings.TrimSpace(command.Name)
				if trimmed == "" {
					continue
				}
				route.commands[strings.ToLower(trimmed)] = trimmed
			}
			routes[topic] = route
		}
	}
	p.mu.Lock()
	p.routes = routes
	p.mu.Unlock()
}

func (p *Publisher) lookupRoute(topic, command string) (commandRoute, string, bool) {
	p.mu.Lock()
	route, ok := p.routes[topic]
	p.mu.Unlock()
	if !ok {
		return commandRoute{}, "", false
	}
	if len(route.commands) == 0 {
		return route, command, true
	}
	mappedCommand, ok := route.commands[strings.ToLower(strings.TrimSpace(command))]
	if !ok {
		return commandRoute{}, "", false
	}
	return route, mappedCommand, true
}

func (p *Publisher) publish(ctx context.Context, message PublishMessage, discovery bool) error {
	now := p.now().UTC()
	p.mu.Lock()
	defer p.mu.Unlock()
	if discovery {
		if _, ok := p.discovery[message.Topic]; ok {
			return nil
		}
	}
	if previous, ok := p.published[message.Topic]; ok {
		if bytes.Equal(previous.payload, message.Payload) && now.Sub(previous.at) < p.cfg.Heartbeat {
			return nil
		}
	}
	if _, err := p.client.Publish(ctx, &paho.Publish{QoS: 1, Retain: message.Retain, Topic: message.Topic, Payload: message.Payload}); err != nil {
		return err
	}
	clonedPayload := append([]byte(nil), message.Payload...)
	p.published[message.Topic] = publishedState{payload: clonedPayload, at: now}
	if discovery {
		p.discovery[message.Topic] = struct{}{}
	}
	if p.logger != nil {
		p.logger.Printf("mqtt publish topic=%s retain=%t bytes=%d", message.Topic, message.Retain, len(message.Payload))
	}
	return nil
}

func heartbeatOrDefault(value time.Duration) time.Duration {
	if value <= 0 {
		return defaultHeartbeat
	}
	return value
}
