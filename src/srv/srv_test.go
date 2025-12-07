package srv

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

func mockAddrsLookup(service, proto, name string) (cname string, addrs []*net.SRV, err error) {
	return "ignored", []*net.SRV{
		{Target: "z", Port: 1, Priority: 0, Weight: 0},
		{Target: "z", Port: 0, Priority: 0, Weight: 0},
		{Target: "a", Port: 9001, Priority: 0, Weight: 0},
	}, nil
}

func TestLookupServerStringsFromSrvReturnsServersSorted(t *testing.T) {
	targets, err := lookupServerStringsFromSrv("_something._tcp.example.org.", mockAddrsLookup)
	assert.Nil(t, err)
	assert.Equal(t, targets, []string{"a:9001", "z:0", "z:1"})
}
