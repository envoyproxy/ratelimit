package memcached

import (
	"errors"
	"net"
	"testing"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/stretchr/testify/assert"

	"github.com/golang/mock/gomock"

	mock_srv "github.com/envoyproxy/ratelimit/test/mocks/srv"
)

func TestRefreshServersSetsServersOnEmptyServerList(t *testing.T) {
	assert := assert.New(t)

	mockSrv := "_memcache._tcp.example.org"
	mockMemcacheHostPort := "127.0.0.1:11211"
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSrvResolver := mock_srv.NewMockSrvResolver(ctrl)

	mockSrvResolver.EXPECT().ServerStringsFromSrv(gomock.Eq(mockSrv)).Return([]string{mockMemcacheHostPort}, nil)

	serverList := new(memcache.ServerList)

	refreshServers(serverList, mockSrv, mockSrvResolver)

	actualMemcacheHosts := []string{}

	serverList.Each(func(addr net.Addr) error {
		actualMemcacheHosts = append(actualMemcacheHosts, addr.String())
		return nil
	})

	assert.Equal([]string{mockMemcacheHostPort}, actualMemcacheHosts)
}

func TestRefreshServersOverridesServersOnNonEmptyServerList(t *testing.T) {
	assert := assert.New(t)

	mockSrv := "_memcache._tcp.example.org"
	mockMemcacheHostPort := "127.0.0.1:11211"
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSrvResolver := mock_srv.NewMockSrvResolver(ctrl)

	mockSrvResolver.EXPECT().ServerStringsFromSrv(gomock.Eq(mockSrv)).Return([]string{mockMemcacheHostPort}, nil)

	serverList := new(memcache.ServerList)
	serverList.SetServers("127.0.0.2:11211", "127.0.0.3:11211")

	refreshServers(serverList, mockSrv, mockSrvResolver)

	actualMemcacheHosts := []string{}

	serverList.Each(func(addr net.Addr) error {
		actualMemcacheHosts = append(actualMemcacheHosts, addr.String())
		return nil
	})

	assert.Equal([]string{mockMemcacheHostPort}, actualMemcacheHosts)
}

func TestRefreshServerSetsServersDoesNotChangeAnythingIfThereIsAnError(t *testing.T) {
	assert := assert.New(t)

	mockSrv := "_memcache._tcp.example.org"
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSrvResolver := mock_srv.NewMockSrvResolver(ctrl)

	mockSrvResolver.EXPECT().ServerStringsFromSrv(gomock.Eq(mockSrv)).Return(nil, errors.New("some error"))

	originalServers := []string{"127.0.0.2:11211", "127.0.0.3:11211"}
	serverList := new(memcache.ServerList)
	serverList.SetServers(originalServers...)

	refreshServers(serverList, mockSrv, mockSrvResolver)

	actualMemcacheHosts := []string{}

	serverList.Each(func(addr net.Addr) error {
		actualMemcacheHosts = append(actualMemcacheHosts, addr.String())
		return nil
	})

	assert.Equal(originalServers, actualMemcacheHosts)
}
