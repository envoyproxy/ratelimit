package srv

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/srv"
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
	srvResolver := srv.DnsSrvResolver{}
	_, err := srvResolver.ServerStringsFromSrv("example.org")
	assert.Equal(t, err, errors.New("could not parse example.org to SRV parts"))
}

func TestServerStringsFromSevWhenSrvIsWellFormedButNotLookupable(t *testing.T) {
	srvResolver := srv.DnsSrvResolver{}
	_, err := srvResolver.ServerStringsFromSrv("_something._tcp.example.invalid")
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
	srvResolver := srv.DnsSrvResolver{}
	servers, err := srvResolver.ServerStringsFromSrv("_imaps._tcp.gmail.com.")
	assert.Nil(t, err)
	assert.True(t, len(servers) > 0)
	for _, s := range servers {
		assert.Regexp(t, `^.*imap.*gmail.com.:\d+$`, s)
	}
}
