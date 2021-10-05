package metrics

import (
	"context"
	"testing"
	"time"

	stats "github.com/lyft/gostats"
	statsMock "github.com/lyft/gostats/mock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/envoyproxy/ratelimit/src/metrics"
)

func TestMetricsInterceptor(t *testing.T) {
	mockSink := statsMock.NewSink()
	statsStore := stats.NewStore(mockSink, false)
	serverReporter := metrics.NewServerReporter(statsStore)

	unaryInfo := &grpc.UnaryServerInfo{
		FullMethod: "TestService/TestMethod",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		time.Sleep(100 * time.Millisecond)
		return req, nil
	}

	ctx := context.Background()
	interceptor := serverReporter.UnaryServerInterceptor()

	var iterations uint64 = 5

	for i := uint64(0); i < iterations; i++ {
		_, err := interceptor(ctx, nil, unaryInfo, handler)
		assert.NoError(t, err)
	}

	totalRequestsCounter := statsStore.NewCounter("TestMethod.total_requests")
	assert.Equal(t, iterations, totalRequestsCounter.Value())
	assert.True(t, mockSink.Timer("TestMethod.response_time") >= float64(iterations*100))
}

func TestMetricsInterceptor_Concurrent(t *testing.T) {
	mockSink := statsMock.NewSink()
	statsStore := stats.NewStore(mockSink, false)
	serverReporter := metrics.NewServerReporter(statsStore)

	unaryInfo := &grpc.UnaryServerInfo{
		FullMethod: "TestService/TestMethod",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return req, nil
	}

	ctx := context.Background()
	interceptor := serverReporter.UnaryServerInterceptor()

	var iterations uint64 = 50
	c := make(chan bool)

	go func() {
		for i := uint64(0); i < iterations; i++ {
			_, err := interceptor(ctx, nil, unaryInfo, handler)
			assert.NoError(t, err)
		}
		c <- true
	}()

	go func() {
		for i := uint64(0); i < iterations; i++ {
			_, err := interceptor(ctx, nil, unaryInfo, handler)
			assert.NoError(t, err)
		}
		c <- true
	}()

	for i := 0; i < 2; i++ {
		<-c
	}

	totalRequestsCounter := statsStore.NewCounter("TestMethod.total_requests")
	assert.Equal(t, iterations*2, totalRequestsCounter.Value())
	// verify that timer exists in the sink
	assert.NotEqual(t, 0, mockSink.Timer("TestMethod.response_time"))
}
