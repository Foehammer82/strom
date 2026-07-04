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
}

type NodeSnapshot struct {
	Node  NodeInfo
	UPSes []UPSInfo
}

type publishClient interface {
	AwaitConnection(context.Context) error
	Publish(context.Context, *paho.Publish) (*paho.PublishResponse, error)
}

type publishedState struct {
	payload []byte
	at      time.Time
}

type Publisher struct {
	logger    *log.Logger
	cfg       RuntimeConfig
	client    publishClient
	now       func() time.Time
	mu        sync.Mutex
	published map[string]publishedState
	discovery map[string]struct{}
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
		clientID = "wattkeeper-controller"
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
	return &Publisher{
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
		published: map[string]publishedState{},
		discovery: map[string]struct{}{},
	}, nil
}

func NewTestPublisher(cfg RuntimeConfig, client publishClient, now func() time.Time) *Publisher {
	return &Publisher{
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
		published: map[string]publishedState{},
		discovery: map[string]struct{}{},
	}
}

func (p *Publisher) PublishSnapshots(ctx context.Context, snapshots []NodeSnapshot) error {
	if p == nil || p.client == nil {
		return nil
	}
	if err := p.client.AwaitConnection(ctx); err != nil {
		return err
	}
	if err := p.publish(ctx, PublishMessage{Topic: p.cfg.StatePrefix + "/controller/availability", Payload: []byte("online"), Retain: true}, true); err != nil {
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
