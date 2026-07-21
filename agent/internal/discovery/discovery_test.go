package discovery

import (
	"errors"
	"reflect"
	"testing"
)

func TestResolveIdentityPrefersCPUInfoSerial(t *testing.T) {
	t.Parallel()

	identity, err := resolveIdentity(func(path string) ([]byte, error) {
		switch path {
		case procCPUInfoPath:
			return []byte("Hardware\t: BCM2710\nSerial\t\t: 00000000ABCDEF12\n"), nil
		default:
			return nil, errors.New("unexpected path")
		}
	})
	if err != nil {
		t.Fatalf("resolveIdentity() error = %v", err)
	}

	if identity.Serial != "00000000abcdef12" {
		t.Fatalf("Serial = %q, want %q", identity.Serial, "00000000abcdef12")
	}
	if identity.Instance != "strom-node-ef12" {
		t.Fatalf("Instance = %q, want %q", identity.Instance, "strom-node-ef12")
	}
}

func TestResolveIdentityFallsBackToMachineID(t *testing.T) {
	t.Parallel()

	identity, err := resolveIdentity(func(path string) ([]byte, error) {
		switch path {
		case procCPUInfoPath, devTreeSerialPath:
			return nil, errors.New("missing")
		case machineIDPath:
			return []byte("MACHINE-ID-1234\n"), nil
		default:
			return nil, errors.New("unexpected path")
		}
	})
	if err != nil {
		t.Fatalf("resolveIdentity() error = %v", err)
	}

	if identity.Serial != "machine-id-1234" {
		t.Fatalf("Serial = %q, want %q", identity.Serial, "machine-id-1234")
	}
	if identity.Instance != "strom-node-1234" {
		t.Fatalf("Instance = %q, want %q", identity.Instance, "strom-node-1234")
	}
}

func TestAdvertiserUpdatesTXTOnlyWhenCountChanges(t *testing.T) {
	t.Parallel()

	announcement := &fakeAnnouncement{}
	advertiser := &Advertiser{
		meta: Metadata{Serial: "serial1234", Instance: "strom-node-1234", Version: "1.2.3", Port: 8080},
		reg:  fakeRegistrar{announcement: announcement},
	}

	if err := advertiser.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	advertiser.UpdateUPSCount(2)
	advertiser.UpdateUPSCount(2)
	advertiser.UpdateAdopted(true)
	advertiser.UpdateUPSCount(3)
	advertiser.Close()

	if len(announcement.texts) != 4 {
		t.Fatalf("TXT updates = %d, want 4", len(announcement.texts))
	}

	want := [][]string{
		{"id=serial1234", "adopted=false", "ups_count=0", "version=1.2.3"},
		{"id=serial1234", "adopted=false", "ups_count=2", "version=1.2.3"},
		{"id=serial1234", "adopted=true", "ups_count=2", "version=1.2.3"},
		{"id=serial1234", "adopted=true", "ups_count=3", "version=1.2.3"},
	}
	if !reflect.DeepEqual(announcement.texts, want) {
		t.Fatalf("TXT records = %#v, want %#v", announcement.texts, want)
	}
	if !announcement.shutdown {
		t.Fatal("expected Shutdown() to be called")
	}
}

func TestAdvertiserRegistersLatestMetadataAfterDelayedStart(t *testing.T) {
	t.Parallel()

	announcement := &fakeAnnouncement{}
	advertiser := &Advertiser{
		meta: Metadata{Serial: "serial1234", Instance: "strom-node-1234", Version: "1.2.3", Port: 8080},
		reg:  fakeRegistrar{announcement: announcement},
	}

	advertiser.UpdateUPSCount(2)
	advertiser.UpdateAdopted(true)
	if err := advertiser.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	want := [][]string{{"id=serial1234", "adopted=true", "ups_count=2", "version=1.2.3"}}
	if !reflect.DeepEqual(announcement.texts, want) {
		t.Fatalf("TXT records = %#v, want %#v", announcement.texts, want)
	}
}

type fakeRegistrar struct {
	announcement *fakeAnnouncement
}

func (f fakeRegistrar) Register(instance, service, domain string, port int, text []string) (serviceAnnouncement, error) {
	f.announcement.texts = append(f.announcement.texts, append([]string(nil), text...))
	return f.announcement, nil
}

type fakeAnnouncement struct {
	texts    [][]string
	shutdown bool
}

func (f *fakeAnnouncement) SetText(text []string) {
	f.texts = append(f.texts, append([]string(nil), text...))
}

func (f *fakeAnnouncement) Shutdown() {
	f.shutdown = true
}
