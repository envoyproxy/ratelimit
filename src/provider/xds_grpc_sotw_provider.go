package provider

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/grpc/metadata"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	"github.com/golang/protobuf/ptypes/any"
	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	logger "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ratelimit/src/config"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"

	"github.com/envoyproxy/go-control-plane/pkg/client/sotw/v3"
	rls_conf_v3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
)

// XdsGrpcSotwProvider is the xDS provider which implements `RateLimitConfigProvider` interface.
type XdsGrpcSotwProvider struct {
	settings              settings.Settings
	loader                config.RateLimitConfigLoader
	configUpdateEventChan chan ConfigUpdateEvent
	statsManager          stats.Manager
	ctx                   context.Context
	adsClient             sotw.ADSClient
	// connectionRetryChannel is the channel which trigger true for connection issues
	connectionRetryChannel chan bool
}

// NewXdsGrpcSotwProvider initializes xDS listener and returns the xDS provider.
func NewXdsGrpcSotwProvider(settings settings.Settings, statsManager stats.Manager) RateLimitConfigProvider {
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.New(settings.ConfigGrpcXdsClientAdditionalHeaders))
	p := &XdsGrpcSotwProvider{
		settings:               settings,
		statsManager:           statsManager,
		ctx:                    ctx,
		configUpdateEventChan:  make(chan ConfigUpdateEvent),
		connectionRetryChannel: make(chan bool),
		loader:                 config.NewRateLimitConfigLoaderImpl(),
		adsClient:              sotw.NewADSClient(ctx, getClientNode(settings), resource.RateLimitConfigType),
	}
	go p.initXdsClient()
	return p
}

// ConfigUpdateEvent returns config provider channel
func (p *XdsGrpcSotwProvider) ConfigUpdateEvent() <-chan ConfigUpdateEvent {
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
	conn, err := p.getGrpcConnection()
	if err != nil {
		logger.Errorf("Error initializing gRPC connection to xDS Management Server: %s", err.Error())
		p.retryGrpcConn()
		return nil
	}

	logger.Info("Connection to xDS Management Server is successful")
	p.adsClient.InitConnect(conn)
	go p.watchConfigs()
	return conn
}

func (p *XdsGrpcSotwProvider) watchConfigs() {
	for {
		resp, err := p.adsClient.Fetch()
		if err != nil {
			logger.Errorf("Failed to receive configuration from xDS Management Server: %s", err.Error())
			if sotw.IsConnError(err) {
				p.retryGrpcConn()
				return
			}
			p.adsClient.Nack(err.Error())
		} else {
			logger.Tracef("Response received from xDS Management Server: %v", resp)
			p.sendConfigs(resp.Resources)
		}
	}
}

func (p *XdsGrpcSotwProvider) getGrpcConnection() (*grpc.ClientConn, error) {
	backOff := grpc_retry.BackoffLinearWithJitter(p.settings.ConfigGrpcXdsServerConnectRetryInterval, 0.5)
	logger.Infof("Dialing xDS Management Server: '%s'", p.settings.ConfigGrpcXdsServerUrl)
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
		logger.Infof("ServerName used for xDS Management Service hostname verification is %s", p.settings.ConfigGrpcXdsServerTlsSAN)
		configGrpcXdsTlsConfig.ServerName = p.settings.ConfigGrpcXdsServerTlsSAN
	}
	return grpc.WithTransportCredentials(credentials.NewTLS(configGrpcXdsTlsConfig))
}

func (p *XdsGrpcSotwProvider) sendConfigs(resources []*any.Any) {
	defer func() {
		if e := recover(); e != nil {
			p.configUpdateEventChan <- &ConfigUpdateEventImpl{err: e}
			p.adsClient.Nack(fmt.Sprint(e))
		}
	}()

	conf := make([]config.RateLimitConfigToLoad, 0, len(resources))
	for _, res := range resources {
		confPb := &rls_conf_v3.RateLimitConfig{}
		err := anypb.UnmarshalTo(res, confPb, proto.UnmarshalOptions{})
		if err != nil {
			logger.Errorf("Error while unmarshalling config from xDS Management Server: %s", err.Error())
			p.adsClient.Nack(err.Error())
			return
		}

		configYaml := config.ConfigXdsProtoToYaml(confPb)
		conf = append(conf, config.RateLimitConfigToLoad{Name: confPb.Name, ConfigYaml: configYaml})
	}
	rlSettings := settings.NewSettings()
	rlsConf := p.loader.Load(conf, p.statsManager, rlSettings.MergeDomainConfigurations)
	p.configUpdateEventChan <- &ConfigUpdateEventImpl{config: rlsConf}
	p.adsClient.Ack()
}

func (p *XdsGrpcSotwProvider) retryGrpcConn() {
	p.connectionRetryChannel <- true
}

func getClientNode(s settings.Settings) *corev3.Node {
	// setting metadata for node
	metadataMap := make(map[string]*structpb.Value)
	for _, entry := range strings.Split(s.ConfigGrpcXdsNodeMetadata, ",") {
		keyValPair := strings.SplitN(entry, "=", 2)
		if len(keyValPair) == 2 {
			metadataMap[keyValPair[0]] = structpb.NewStringValue(keyValPair[1])
		}
	}

	return &corev3.Node{
		Id:       s.ConfigGrpcXdsNodeId,
		Metadata: &structpb.Struct{Fields: metadataMap},
	}
}
