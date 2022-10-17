package common

import (
	"context"
	"fmt"
	"net"
	"testing"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"

	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
)

type XdsServerConfig struct {
	Port   int
	NodeId string
}

func StartXdsSotwServer(t *testing.T, config *XdsServerConfig, initSnapshot *cache.Snapshot) (cache.SnapshotCache, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	snapCache := cache.NewSnapshotCache(true, cache.IDHash{}, nil) // TODO: marked as true
	if err := initSnapshot.Consistent(); err != nil {
		t.Errorf("Error checking consistency in initial snapshot: %v", err)
	}

	if err := snapCache.SetSnapshot(context.Background(), config.NodeId, initSnapshot); err != nil {
		panic(err)
	}
	srv := server.NewServer(ctx, snapCache, nil)

	grpcServer := grpc.NewServer()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
	if err != nil {
		t.Errorf("Error listening to port: %v: %v", config.Port, err)
	}
	discoverygrpc.RegisterAggregatedDiscoveryServiceServer(grpcServer, srv)
	go func() {
		if err = grpcServer.Serve(lis); err != nil {
			t.Error(err)
		}
	}()

	return snapCache, func() {
		cancel()
		grpcServer.Stop()
	}
}
