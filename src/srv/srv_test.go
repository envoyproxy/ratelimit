package srv

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func mockAddrsLookup(service, proto, name string) (cname string, addrs []*net.SRV, err error) {
	return "ignored", []*net.SRV{{"z", 1, 0, 0}, {"z", 0, 0, 0}, {"a", 9001, 0, 0}}, nil
}

func TestLookupServerStringsFromSrvReturnsServersSorted(t *testing.T) {
	targets, err := lookupServerStringsFromSrv("_something._tcp.example.org.", mockAddrsLookup)
	assert.Nil(t, err)
	assert.Equal(t, targets, []string{"a:9001", "z:0", "z:1"})
}
