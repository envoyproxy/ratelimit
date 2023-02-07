package common

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"google.golang.org/grpc"

	discoverygrpc "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
)

type XdsServerConfig struct {
	Port   int
	NodeId string
}

type SetSnapshotFunc func(*cache.Snapshot)

func StartXdsSotwServer(t *testing.T, config *XdsServerConfig, initSnapshot *cache.Snapshot) (SetSnapshotFunc, context.CancelFunc) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	snapCache := cache.NewSnapshotCache(true, cache.IDHash{}, nil)
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

	// HACK: Wait for the server to come up. Make a hook that we can wait on.
	WaitForTcpPort(context.Background(), config.Port, 1*time.Second)

	cancelFunc := func() {
		cancel()
		grpcServer.Stop()
	}
	return setSnapshotFunc(t, snapCache, config.NodeId), cancelFunc
}

func setSnapshotFunc(t *testing.T, snapCache cache.SnapshotCache, nodeId string) SetSnapshotFunc {
	return func(snapshot *cache.Snapshot) {
		if err := snapshot.Consistent(); err != nil {
			t.Errorf("snapshot inconsistency: %+v\n%+v", snapshot, err)
		}
		if err := snapCache.SetSnapshot(context.Background(), nodeId, snapshot); err != nil {
			t.Errorf("snapshot error %q for %+v", err, snapshot)
		}
	}
}
