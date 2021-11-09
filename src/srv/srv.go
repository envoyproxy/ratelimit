package srv

import (
	"errors"
	"fmt"
	"net"
	"regexp"

	logger "github.com/sirupsen/logrus"
)

var srvRegex = regexp.MustCompile(`^_(.+?)\._(.+?)\.(.+)$`)

type SrvResolver interface {
	ServerStringsFromSrv(srv string) ([]string, error)
}

type DnsSrvResolver struct{}

func ParseSrv(srv string) (string, string, string, error) {
	matches := srvRegex.FindStringSubmatch(srv)
	if matches == nil {
		errorText := fmt.Sprintf("could not parse %s to SRV parts", srv)
		logger.Errorf(errorText)
		return "", "", "", errors.New(errorText)
	}
	return matches[1], matches[2], matches[3], nil
}

func (dnsSrvResolver DnsSrvResolver) ServerStringsFromSrv(srv string) ([]string, error) {
	service, proto, name, err := ParseSrv(srv)
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
