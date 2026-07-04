package discovery

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/grandcat/zeroconf"
)

const (
	defaultDomain     = "local."
	defaultPort       = 80
	defaultService    = "_wattkeeper._tcp"
	devTreeSerialPath = "/sys/firmware/devicetree/base/serial-number"
	machineIDPath     = "/etc/machine-id"
	procCPUInfoPath   = "/proc/cpuinfo"
	varMachineIDPath  = "/var/lib/dbus/machine-id"
)

type Identity struct {
	Serial   string
	Instance string
}

type Metadata struct {
	Serial   string
	Instance string
	Version  string
	Adopted  bool
	Port     int
}

type serviceAnnouncement interface {
	SetText([]string)
	Shutdown()
}

type registrar interface {
	Register(instance, service, domain string, port int, text []string) (serviceAnnouncement, error)
}

type zeroconfRegistrar struct{}

func (zeroconfRegistrar) Register(instance, service, domain string, port int, text []string) (serviceAnnouncement, error) {
	return zeroconf.Register(instance, service, domain, port, text, nil)
}

type Advertiser struct {
	mu       sync.Mutex
	logger   *log.Logger
	meta     Metadata
	reg      registrar
	server   serviceAnnouncement
	upsCount int
	started  bool
}

func ResolveIdentity() (Identity, error) {
	return resolveIdentity(os.ReadFile)
}

func IdentityForSerial(serial string) Identity {
	normalized := normalizeIdentifier(serial)
	return Identity{
		Serial:   normalized,
		Instance: instanceName(normalized),
	}
}

func NewAdvertiser(logger *log.Logger, meta Metadata) *Advertiser {
	if meta.Port == 0 {
		meta.Port = defaultPort
	}
	return &Advertiser{
		logger: logger,
		meta:   meta,
		reg:    zeroconfRegistrar{},
	}
}

func (a *Advertiser) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return nil
	}

	server, err := a.reg.Register(a.meta.Instance, defaultService, defaultDomain, a.meta.Port, txtRecords(a.meta, a.upsCount))
	if err != nil {
		return fmt.Errorf("register mDNS service: %w", err)
	}

	a.server = server
	a.started = true
	if a.logger != nil {
		a.logger.Printf("mDNS advertisement started instance=%s service=%s port=%d", a.meta.Instance, defaultService, a.meta.Port)
	}

	return nil
}

func (a *Advertiser) UpdateUPSCount(count int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started || a.server == nil || count == a.upsCount {
		return
	}

	a.upsCount = count
	a.server.SetText(txtRecords(a.meta, a.upsCount))
	if a.logger != nil {
		a.logger.Printf("mDNS advertisement updated instance=%s ups_count=%d", a.meta.Instance, a.upsCount)
	}
}

func (a *Advertiser) UpdateAdopted(adopted bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.meta.Adopted == adopted {
		return
	}
	a.meta.Adopted = adopted
	if !a.started || a.server == nil {
		return
	}
	a.server.SetText(txtRecords(a.meta, a.upsCount))
	if a.logger != nil {
		a.logger.Printf("mDNS advertisement updated instance=%s adopted=%t", a.meta.Instance, a.meta.Adopted)
	}
}

func (a *Advertiser) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server != nil {
		a.server.Shutdown()
		a.server = nil
	}
	a.started = false
}

func resolveIdentity(readFile func(string) ([]byte, error)) (Identity, error) {
	serial, err := resolveSerial(readFile)
	if err != nil {
		return Identity{}, err
	}

	return Identity{
		Serial:   serial,
		Instance: instanceName(serial),
	}, nil
}

func resolveSerial(readFile func(string) ([]byte, error)) (string, error) {
	if serial, err := serialFromCPUInfo(readFile); err == nil && serial != "" {
		return serial, nil
	}

	if serial, err := readTrimmed(readFile, devTreeSerialPath); err == nil && serial != "" {
		return serial, nil
	}

	for _, path := range []string{machineIDPath, varMachineIDPath} {
		serial, err := readTrimmed(readFile, path)
		if err == nil && serial != "" {
			return serial, nil
		}
	}

	return "", fmt.Errorf("resolve node serial from %s, %s, or machine-id", procCPUInfoPath, devTreeSerialPath)
}

func serialFromCPUInfo(readFile func(string) ([]byte, error)) (string, error) {
	content, err := readFile(procCPUInfoPath)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		key, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "Serial") {
			continue
		}
		serial := normalizeIdentifier(value)
		if serial != "" {
			return serial, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", fmt.Errorf("serial line not found in %s", procCPUInfoPath)
}

func readTrimmed(readFile func(string) ([]byte, error), path string) (string, error) {
	content, err := readFile(path)
	if err != nil {
		return "", err
	}

	value := normalizeIdentifier(string(content))
	if value == "" {
		return "", fmt.Errorf("empty identifier in %s", path)
	}
	return value, nil
}

func normalizeIdentifier(raw string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(raw, "\x00", ""))
	return strings.ToLower(trimmed)
}

func instanceName(serial string) string {
	identifier := normalizeIdentifier(serial)
	if len(identifier) > 4 {
		identifier = identifier[len(identifier)-4:]
	}
	return "wkeeper-node-" + identifier
}

func txtRecords(meta Metadata, upsCount int) []string {
	version := meta.Version
	if version == "" {
		version = "dev"
	}

	return []string{
		"id=" + meta.Serial,
		"adopted=" + strconv.FormatBool(meta.Adopted),
		"ups_count=" + strconv.Itoa(upsCount),
		"version=" + version,
	}
}
