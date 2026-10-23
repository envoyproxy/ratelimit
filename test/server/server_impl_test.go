package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	gostats "github.com/lyft/gostats"
	"github.com/stretchr/testify/mock"
	"google.golang.org/protobuf/proto"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/server"
	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	mock_v3 "github.com/envoyproxy/ratelimit/test/mocks/rls"
)

func TestMemcacheServerStartsHealthy(t *testing.T) {
	defer signal.Reset(syscall.SIGTERM)

	runtimeRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(runtimeRoot, "config"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := settings.NewSettings()
	s.BackendType = "memcache"
	s.RuntimePath = runtimeRoot
	s.RuntimeSubdirectory = ""
	s.RuntimeAppDirectory = "config"
	s.RuntimeWatchRoot = false

	store := gostats.NewStore(gostats.NewNullSink(), false)
	statsManager := stats.NewStatManager(store, s)
	srv := server.NewServer(s, "ratelimit", statsManager, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthcheck", nil)
	srv.HealthChecker().ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
}

func assertHttpResponse(t *testing.T,
	handler http.HandlerFunc,
	requestBody string,
	expectedStatusCode int,
	expectedContentType string,
	expectedResponseBody string,
) {
	t.Helper()
	assert := assert.New(t)

	req := httptest.NewRequest("METHOD_NOT_CHECKED", "/path_not_checked", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	handler(w, req)

	resp := w.Result()
	actualBody, _ := io.ReadAll(resp.Body)
	assert.Equal(expectedContentType, resp.Header.Get("Content-Type"))
	assert.Equal(expectedStatusCode, resp.StatusCode)
	assert.Equal(expectedResponseBody, string(actualBody))
}

func TestJsonHandler(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	rls := mock_v3.NewMockRateLimitServiceServer(controller)
	handler := server.NewJsonHandler(rls)
	requestMatcher := mock.MatchedBy(func(req *pb.RateLimitRequest) bool {
		return proto.Equal(req, &pb.RateLimitRequest{
			Domain: "foo",
		})
	})

	// Missing request body
	assertHttpResponse(t, handler, "", 400, "text/plain; charset=utf-8", "Bad Request\n")

	// Request body is not valid json
	assertHttpResponse(t, handler, "}", 400, "text/plain; charset=utf-8", "Bad Request\n")

	// Unknown response code
	rls.EXPECT().ShouldRateLimit(context.Background(), requestMatcher).Return(&pb.RateLimitResponse{}, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 500, "application/json", "{}")

	// ratelimit service error
	rls.EXPECT().ShouldRateLimit(context.Background(), requestMatcher).Return(nil, fmt.Errorf("some error"))
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 400, "text/plain; charset=utf-8", "Bad Request\n")

	// json unmarshaling error
	rls.EXPECT().ShouldRateLimit(context.Background(), requestMatcher).Return(nil, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 500, "text/plain; charset=utf-8", "Internal Server Error\n")

	// successful request, not rate limited
	rls.EXPECT().ShouldRateLimit(context.Background(), requestMatcher).Return(&pb.RateLimitResponse{
		OverallCode: pb.RateLimitResponse_OK,
	}, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 200, "application/json", `{"overallCode":"OK"}`)

	// successful request, rate limited
	rls.EXPECT().ShouldRateLimit(context.Background(), requestMatcher).Return(&pb.RateLimitResponse{
		OverallCode: pb.RateLimitResponse_OVER_LIMIT,
	}, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 429, "application/json", `{"overallCode":"OVER_LIMIT"}`)
}
