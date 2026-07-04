package browse

import (
	"net"
	"testing"

	"github.com/grandcat/zeroconf"
)

func TestParseEntryExtractsNodeMetadata(t *testing.T) {
	t.Parallel()

	entry := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: "wkeeper-node-1234"},
		HostName:      "wkeeper-node-1234.local.",
		Port:          80,
		Text:          []string{"id=serial-1234", "adopted=false", "ups_count=2", "version=v0.3.0"},
		AddrIPv4:      []net.IP{net.ParseIP("192.168.1.50")},
	}

	node, ok := parseEntry(entry)
	if !ok {
		t.Fatal("parseEntry() ok = false, want true")
	}
	if node.ID != "serial-1234" || node.Instance != "wkeeper-node-1234" || node.Address != "192.168.1.50" || node.UPSCount != 2 {
		t.Fatalf("node = %#v, want parsed live node", node)
	}
	if node.Adopted {
		t.Fatalf("Adopted = %t, want false", node.Adopted)
	}
}

func TestParseEntryRejectsMissingID(t *testing.T) {
	t.Parallel()

	if _, ok := parseEntry(&zeroconf.ServiceEntry{Text: []string{"ups_count=1"}}); ok {
		t.Fatal("parseEntry() ok = true, want false")
	}
}
