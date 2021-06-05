package factory

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/envoyproxy/ratelimit/src/storage/service"
	"github.com/envoyproxy/ratelimit/src/storage/strategy"
	"github.com/envoyproxy/ratelimit/src/storage/utils"

	stats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

func NewMemcached(scope stats.Scope, hosts []string, srv string, srvRefresh time.Duration, maxIdleConnection int) strategy.StorageStrategy {
	var client service.MemcachedClientInterface

	if srv != "" && len(hosts) > 0 {
		panic(utils.MemcacheError("Both MEMCADHE_HOST_PORT and MEMCACHE_SRV are set"))
	}

	if srv != "" {
		client = newMemcachedClientFromSrv(scope, srv, srvRefresh, maxIdleConnection)
	} else {

	}
	client = newMemcachedClient(scope, hosts, maxIdleConnection)
	return strategy.MemcachedStrategy{
		Client: client,
	}
}

func newMemcachedClient(scope stats.Scope, hosts []string, maxIdleConnection int) service.MemcachedClientInterface {
	client := memcache.New(hosts...)
	client.MaxIdleConns = maxIdleConnection
	stats := service.NewMemcachedStats(scope)
	return &service.MemcachedClient{
		Client: client,
		Stats:  stats,
	}
}

func newMemcachedClientFromSrv(scope stats.Scope, srv string, srvRefresh time.Duration, maxIdleConnection int) service.MemcachedClientInterface {
	serverList := new(memcache.ServerList)
	err := refreshServers(*serverList, srv)
	if err != nil {
		errorText := "Unable to fetch servers from SRV"
		logger.Errorf(errorText)
		panic(utils.MemcacheError(errorText))
	}

	if srvRefresh > 0 {
		logger.Infof("refreshing memcache hosts every: %v milliseconds", srvRefresh.Milliseconds())
		finish := make(chan struct{})
		go refreshServersPeriodically(*serverList, srv, srvRefresh, finish)
	} else {
		logger.Debugf("not periodically refreshing memcached hosts")
	}

	stats := service.NewMemcachedStats(scope)

	return &service.MemcachedClient{
		Client: memcache.NewFromSelector(serverList),
		Stats:  stats,
	}
}

func refreshServers(serverList memcache.ServerList, srv string) error {
	servers, err := serverStringsFromSrv(srv)
	if err != nil {
		return err
	}
	err = serverList.SetServers(servers...)
	if err != nil {
		return err
	}
	return nil
}

func refreshServersPeriodically(serverList memcache.ServerList, srv string, d time.Duration, finish <-chan struct{}) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			err := refreshServers(serverList, srv)
			if err != nil {
				logger.Warn("failed to refresh memcahce hosts")
			} else {
				logger.Debug("refreshed memcache hosts")
			}
		case <-finish:
			return
		}
	}
}

var srvRegex = regexp.MustCompile(`^_(.+?)\._(.+?)\.(.+)$`)

func serverStringsFromSrv(srv string) ([]string, error) {
	service, proto, name, err := parseSrv(srv)

	if err != nil {
		logger.Errorf("failed to parse SRV: %s", err)
		return nil, err
	}

	_, srvs, err := net.LookupSRV(service, proto, name)

	if err != nil {
		logger.Errorf("failed to lookup SRV: %s", err)
		return nil, err
	}

	logger.Debugf("found %v servers(s) from SRV", len(srvs))

	serversFromSrv := make([]string, len(srvs))
	for i, srv := range srvs {
		server := fmt.Sprintf("%s:%v", srv.Target, srv.Port)
		logger.Debugf("server from srv[%v]: %s", i, server)
		serversFromSrv[i] = fmt.Sprintf("%s:%v", srv.Target, srv.Port)
	}

	return serversFromSrv, nil
}

func parseSrv(srv string) (string, string, string, error) {
	matches := srvRegex.FindStringSubmatch(srv)
	if matches == nil {
		errorText := fmt.Sprintf("could not parse %s to SRV parts", srv)
		logger.Errorf(errorText)
		return "", "", "", errors.New(errorText)
	}
	return matches[1], matches[2], matches[3], nil
}
