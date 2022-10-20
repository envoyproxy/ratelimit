// Copyright 2020 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.
package main

import (
	"context"
	"flag"
	"os"

	"github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	"github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/envoyproxy/go-control-plane/pkg/test/v3"

	example "github.com/envoyproxy/ratelimit/examples/xds-sotw-config-server"
)

var (
	logger example.Logger
	port   uint
	nodeID string
)

func init() {
	logger = example.Logger{}

	flag.BoolVar(&logger.Debug, "debug", false, "Enable xDS server debug logging")
	flag.UintVar(&port, "port", 18000, "xDS management server port")
	flag.StringVar(&nodeID, "nodeID", "test-node-id", "Node ID")
}

func main() {
	flag.Parse()

	// Create a cache
	cache := cache.NewSnapshotCache(false, cache.IDHash{}, logger)

	// Create the snapshot that we'll serve to Envoy
	snapshot := example.GenerateSnapshot()
	if err := snapshot.Consistent(); err != nil {
		logger.Errorf("Snapshot is inconsistent: %+v\n%+v", snapshot, err)
		os.Exit(1)
	}
	logger.Debugf("Will serve snapshot %+v", snapshot)

	// Add the snapshot to the cache
	if err := cache.SetSnapshot(context.Background(), nodeID, snapshot); err != nil {
		logger.Errorf("Snapshot error %q for %+v", err, snapshot)
		os.Exit(1)
	}

	// Run the xDS server
	ctx := context.Background()
	cb := &test.Callbacks{Debug: logger.Debug}
	srv := server.NewServer(ctx, cache, cb)
	example.RunServer(ctx, srv, port)
}
