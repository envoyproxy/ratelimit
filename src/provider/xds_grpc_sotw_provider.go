package provider

import (
	"context"
	"io"

	core "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	discovery "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/golang/protobuf/ptypes/any"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	logger "github.com/sirupsen/logrus"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"

	"google.golang.org/grpc/codes"
	grpcStatus "google.golang.org/grpc/status"

	rls_conf_v3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	// rls_svc_v3 "github.com/envoyproxy/go-control-plane/ratelimit/service/ratelimit/v3"
)

const (
	configTypeURL string = "type.googleapis.com/ratelimit.config.ratelimit.v3.RateLimitConfig"
)

type XdsGrpcSotwProvider struct {
	settings              settings.Settings
	loader                config.RateLimitConfigLoader
	configUpdateEventChan chan ConfigUpdateEvent
	statsManager          stats.Manager
	// xdsStream             rls_svc_v3.RateLimitConfigDiscoveryService_StreamRlsConfigsClient
	xdsStream         discovery.AggregatedDiscoveryService_StreamAggregatedResourcesClient
	lastAckedResponse *discovery.DiscoveryResponse
	// TODO: (renuka) lastAckedResponse and lastReceivedResponse are equal
	lastReceivedResponse *discovery.DiscoveryResponse
	// If a connection error occurs, true event would be returned
	connectionRetryChannel chan bool
}

func NewXdsGrpcSotwProvider(settings settings.Settings, statsManager stats.Manager) RateLimitConfigProvider {
	return &XdsGrpcSotwProvider{
		settings:               settings,
		statsManager:           statsManager,
		configUpdateEventChan:  make(chan ConfigUpdateEvent),
		connectionRetryChannel: make(chan bool),
		loader:                 config.NewRateLimitConfigLoaderImpl(),
	}
}

func (p *XdsGrpcSotwProvider) ConfigUpdateEvent() <-chan ConfigUpdateEvent {
	go p.initXdsClient()
	return p.configUpdateEventChan
}

func (p *XdsGrpcSotwProvider) Stop() {
	p.connectionRetryChannel <- false
}

func (p *XdsGrpcSotwProvider) initXdsClient() {
	logger.Info("Starting xDS client connection for rate limit configurations")
	conn := p.initializeAndWatch()
	for retryEvent := range p.connectionRetryChannel {
		if conn != nil {
			conn.Close()
		}
		if !retryEvent { // stop watching
			logger.Info("Stopping xDS client watch for rate limit configurations")
			break
		}
		conn = p.initializeAndWatch()
	}
}

func (p *XdsGrpcSotwProvider) initializeAndWatch() *grpc.ClientConn {
	conn, err := p.initConnection()
	if err != nil {
		p.connectionRetryChannel <- true
		return conn
	}
	go p.watchConfigs()

	// TODO: (renuka) check this, no nil for all cases
	var lastAppliedVersion string
	if p.lastAckedResponse != nil {
		// If the connection is interrupted in the middle, we need to apply if the version remains same
		lastAppliedVersion = p.lastAckedResponse.VersionInfo
	} else {
		lastAppliedVersion = ""
	}
	discoveryRequest := &discovery.DiscoveryRequest{
		Node:        &core.Node{Id: p.settings.ConfigGrpcXdsNodeId},
		VersionInfo: lastAppliedVersion,
		TypeUrl:     configTypeURL,
	}
	p.xdsStream.Send(discoveryRequest)
	return conn
}

func (p *XdsGrpcSotwProvider) watchConfigs() {
	for {
		discoveryResponse, err := p.xdsStream.Recv()
		if err == io.EOF {
			// reinitialize again, if stream ends
			logger.Error("EOF is received from xDS Configuration Server")
			p.connectionRetryChannel <- true
			return
		}
		if err != nil {
			logger.Errorf("Failed to receive the discovery response from xDS Configuration Server: %s", err.Error())
			errStatus, _ := grpcStatus.FromError(err)
			if errStatus.Code() == codes.Unavailable || errStatus.Code() == codes.Canceled {
				logger.Errorf("Connection error. errorCode: %s errorMessage: %s",
					errStatus.Code().String(), errStatus.Message())
				p.connectionRetryChannel <- true
				return
			}
			logger.Errorf("Error while xDS communication; errorCode: %s errorMessage: %s",
				errStatus.Code().String(), errStatus.Message())
			p.nack(errStatus.Message())
		} else {
			p.lastReceivedResponse = discoveryResponse
			logger.Debugf("Discovery response is received from xDS Configuration Server with response version: %s", discoveryResponse.VersionInfo)
			logger.Tracef("Discovery response received from xDS Configuration Server: %v", discoveryResponse)
			p.sendConfigs(discoveryResponse.Resources)
		}
	}
}

func (p *XdsGrpcSotwProvider) initConnection() (*grpc.ClientConn, error) {
	conn, err := p.getGrpcConnection()
	if err != nil {
		logger.Errorf("Error initializing gRPC connection to xDS Configuration Server: %s", err.Error())
		return nil, err
	}
	p.xdsStream, err = discovery.NewAggregatedDiscoveryServiceClient(conn).StreamAggregatedResources(context.Background())
	if err != nil {
		logger.Errorf("Error initializing gRPC stream to xDS Configuration Server: %s", err.Error())
		return nil, err
	}
	logger.Info("Connection to xDS Configuration Server is successful")
	return conn, nil
}

func (p *XdsGrpcSotwProvider) getGrpcConnection() (*grpc.ClientConn, error) {
	backOff := grpc_retry.BackoffLinearWithJitter(p.settings.ConfigGrpcXdsServerConnectRetryInterval, 0.5)
	logger.Infof("Dialing xDS Configuration Server: '%s'", p.settings.ConfigGrpcXdsServerUrl)
	return grpc.Dial(
		p.settings.ConfigGrpcXdsServerUrl,
		p.getGrpcTransportCredentials(),
		grpc.WithBlock(),
		grpc.WithStreamInterceptor(
			grpc_retry.StreamClientInterceptor(grpc_retry.WithBackoff(backOff)),
		))
}

func (p *XdsGrpcSotwProvider) getGrpcTransportCredentials() grpc.DialOption {
	if !p.settings.ConfigGrpcXdsServerUseTls {
		return grpc.WithTransportCredentials(insecure.NewCredentials())
	}

	configGrpcXdsTlsConfig := p.settings.ConfigGrpcXdsTlsConfig
	if p.settings.ConfigGrpcXdsServerTlsSAN != "" {
		logger.Infof("ServerName used for xDS configuration service hostname verification is %s", p.settings.ConfigGrpcXdsServerTlsSAN)
		configGrpcXdsTlsConfig.ServerName = p.settings.ConfigGrpcXdsServerTlsSAN
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(configGrpcXdsTlsConfig))
}

func (p *XdsGrpcSotwProvider) sendConfigs(resources []*any.Any) {
	conf := make([]config.RateLimitConfigToLoad, 0, len(resources))
	for _, res := range resources {
		confPb := &rls_conf_v3.RateLimitConfig{}
		err := anypb.UnmarshalTo(res, confPb, proto.UnmarshalOptions{}) // err := ptypes.UnmarshalAny(res, config)
		if err != nil {
			logger.Errorf("Error while unmarshalling config from xDS Configuration Server: %s", err.Error())
			p.nack(err.Error())
			return
		}

		logger.Infof("RENUKA TEST: %v", confPb)

		configYaml := config.ConfigXdsProtoToYaml(confPb)
		conf = append(conf, config.RateLimitConfigToLoad{Name: confPb.Domain, ConfigYaml: configYaml})
	}
	rlSettings := settings.NewSettings()
	rlsConf := p.loader.Load(conf, p.statsManager, rlSettings.MergeDomainConfigurations)
	p.configUpdateEventChan <- &ConfigUpdateEventImpl{config: rlsConf}
	p.ack()
}

func (p *XdsGrpcSotwProvider) ack() {
	p.lastAckedResponse = p.lastReceivedResponse
	discoveryRequest := &discovery.DiscoveryRequest{
		Node:          &core.Node{Id: p.settings.ConfigGrpcXdsNodeId},
		VersionInfo:   p.lastAckedResponse.VersionInfo,
		TypeUrl:       configTypeURL,
		ResponseNonce: p.lastReceivedResponse.Nonce,
	}
	p.xdsStream.Send(discoveryRequest)
}

func (p *XdsGrpcSotwProvider) nack(errorMessage string) {
	discoveryRequest := &discovery.DiscoveryRequest{
		Node:    &core.Node{Id: p.settings.ConfigGrpcXdsNodeId},
		TypeUrl: configTypeURL,
		ErrorDetail: &status.Status{
			Message: errorMessage,
		},
	}
	if p.lastAckedResponse != nil {
		discoveryRequest.VersionInfo = p.lastAckedResponse.VersionInfo
	}
	if p.lastReceivedResponse != nil {
		discoveryRequest.ResponseNonce = p.lastReceivedResponse.Nonce
	}
	p.xdsStream.Send(discoveryRequest)
}
