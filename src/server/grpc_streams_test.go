package server

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/envoyproxy/ratelimit/src/settings"
)

func TestGrpcMaxConcurrentStreamsAdvertised(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit uint32
	}{
		{name: "default unlimited"},
		{name: "configured limit", limit: 16},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := settings.Settings{GrpcMaxConcurrentStreams: tc.limit}
			grpcServer := grpc.NewServer(grpcServerOptions(s)...)
			listener := bufconn.Listen(1024 * 1024)
			serveDone := make(chan error, 1)
			go func() { serveDone <- grpcServer.Serve(listener) }()
			t.Cleanup(func() {
				grpcServer.Stop()
				require.NoError(t, <-serveDone)
			})

			conn, err := listener.Dial()
			require.NoError(t, err)
			defer conn.Close()
			require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
			_, err = io.WriteString(conn, http2.ClientPreface)
			require.NoError(t, err)
			framer := http2.NewFramer(conn, conn)
			require.NoError(t, framer.WriteSettings())
			frame, err := framer.ReadFrame()
			require.NoError(t, err)
			serverSettings, ok := frame.(*http2.SettingsFrame)
			require.True(t, ok, "first server frame must be SETTINGS")

			var advertised uint32
			found := false
			require.NoError(t, serverSettings.ForeachSetting(func(setting http2.Setting) error {
				if setting.ID == http2.SettingMaxConcurrentStreams {
					advertised = setting.Val
					found = true
				}
				return nil
			}))
			if tc.limit == 0 {
				require.False(t, found, "zero must preserve grpc-go's default")
			} else {
				require.True(t, found, "server must advertise the configured cap")
				require.Equal(t, tc.limit, advertised)
			}
		})
	}
}

func TestGrpcStreamLimitWaitsUntilCapacityOrCallerDeadline(t *testing.T) {
	s := settings.Settings{GrpcMaxConcurrentStreams: 1}
	grpcServer := grpc.NewServer(grpcServerOptions(s)...)
	healthpb.RegisterHealthServer(grpcServer, health.NewServer())
	listener := bufconn.Listen(1024 * 1024)
	serveDone := make(chan error, 1)
	go func() { serveDone <- grpcServer.Serve(listener) }()
	t.Cleanup(func() {
		grpcServer.Stop()
		require.NoError(t, <-serveDone)
	})

	clientConn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
	)
	require.NoError(t, err)
	defer clientConn.Close()
	client := healthpb.NewHealthClient(clientConn)
	request := &healthpb.HealthCheckRequest{Service: "ratelimit"}

	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFirst()
	first, err := client.Watch(firstCtx, request)
	require.NoError(t, err)
	_, err = first.Recv()
	require.NoError(t, err) // First Watch now holds the only active stream.

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelSecond()
	second, err := client.Watch(secondCtx, request)
	if err == nil {
		_, err = second.Recv()
	}
	require.Equal(t, codes.DeadlineExceeded, status.Code(err))

	cancelFirst()
	thirdCtx, cancelThird := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelThird()
	third, err := client.Watch(thirdCtx, request)
	require.NoError(t, err)
	_, err = third.Recv()
	require.NoError(t, err)
}
