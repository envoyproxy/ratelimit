package server_test

import (
	"fmt"
	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/mock"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pb "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"

	"github.com/envoyproxy/ratelimit/src/server"
	mock_v3 "github.com/envoyproxy/ratelimit/test/mocks/rls"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func assertHttpResponse(t *testing.T,
	handler http.HandlerFunc,
	requestBody string,
	expectedStatusCode int,
	expectedContentType string,
	expectedResponseBody string) {

	t.Helper()
	assert := assert.New(t)

	req := httptest.NewRequest("METHOD_NOT_CHECKED", "/path_not_checked", strings.NewReader(requestBody))
	w := httptest.NewRecorder()
	handler(w, req)

	resp := w.Result()
	actualBody, _ := ioutil.ReadAll(resp.Body)
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
	assertHttpResponse(t, handler, "", 400, "text/plain; charset=utf-8", "EOF\n")

	// Request body is not valid json
	assertHttpResponse(t, handler, "}", 400, "text/plain; charset=utf-8", "invalid character '}' looking for beginning of value\n")

	// Unknown response code
	rls.EXPECT().ShouldRateLimit(nil, requestMatcher).Return(&pb.RateLimitResponse{}, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 500, "application/json", "{}")

	// ratelimit service error
	rls.EXPECT().ShouldRateLimit(nil, requestMatcher).Return(nil, fmt.Errorf("some error"))
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 400, "text/plain; charset=utf-8", "some error\n")

	// json unmarshaling error
	rls.EXPECT().ShouldRateLimit(nil, requestMatcher).Return(nil, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 500, "text/plain; charset=utf-8", "error marshaling proto3 to json: Marshal called with nil\n")

	// successful request, not rate limited
	rls.EXPECT().ShouldRateLimit(nil, requestMatcher).Return(&pb.RateLimitResponse{
		OverallCode: pb.RateLimitResponse_OK,
	}, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 200, "application/json", `{"overallCode":"OK"}`)

	// successful request, rate limited
	rls.EXPECT().ShouldRateLimit(nil, requestMatcher).Return(&pb.RateLimitResponse{
		OverallCode: pb.RateLimitResponse_OVER_LIMIT,
	}, nil)
	assertHttpResponse(t, handler, `{"domain": "foo"}`, 429, "application/json", `{"overallCode":"OVER_LIMIT"}`)
}
