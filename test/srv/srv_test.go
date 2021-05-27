package srv

import (
	"errors"
	"net"
	"testing"

	"github.com/envoyproxy/ratelimit/src/srv"
	"github.com/stretchr/testify/assert"
)

func TestParseSrv(t *testing.T) {
	service, proto, name, err := srv.ParseSrv("_something._tcp.example.org.")
	assert.Equal(t, service, "something")
	assert.Equal(t, proto, "tcp")
	assert.Equal(t, name, "example.org.")
	assert.Nil(t, err)

	service, proto, name, err = srv.ParseSrv("_something-else._udp.example.org")
	assert.Equal(t, service, "something-else")
	assert.Equal(t, proto, "udp")
	assert.Equal(t, name, "example.org")
	assert.Nil(t, err)

	_, _, _, err = srv.ParseSrv("example.org")
	assert.Equal(t, err, errors.New("could not parse example.org to SRV parts"))
}

func TestServerStringsFromSrvWhenSrvIsNotWellFormed(t *testing.T) {
	_, err := srv.ServerStringsFromSrv("example.org")
	assert.Equal(t, err, errors.New("could not parse example.org to SRV parts"))
}

func TestServerStringsFromSevWhenSrvIsWellFormedButNotLookupable(t *testing.T) {
	_, err := srv.ServerStringsFromSrv("_something._tcp.example.invalid")
	var e *net.DNSError
	if errors.As(err, &e) {
		assert.Equal(t, e.Err, "no such host")
		assert.Equal(t, e.Name, "_something._tcp.example.invalid")
		assert.False(t, e.IsTimeout)
		assert.False(t, e.IsTemporary)
		assert.True(t, e.IsNotFound)
	} else {
		t.Fail()
	}
}

func TestServerStrings(t *testing.T) {
	// it seems reasonable to think _xmpp-server._tcp.gmail.com will be available for a long time!
	servers, err := srv.ServerStringsFromSrv("_xmpp-server._tcp.gmail.com.")
	assert.True(t, len(servers) > 0)
	for _, s := range servers {
		assert.Regexp(t, `^.*xmpp-server.*google.com.:\d+$`, s)
	}
	assert.Nil(t, err)
}
