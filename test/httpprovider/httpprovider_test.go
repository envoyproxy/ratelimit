package httpprovider_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/envoyproxy/ratelimit/src/httpprovider"
)

func TestProvideWithOneEndpoint(t *testing.T) {
	assert := assert.New(t)

	testConfig, _ := os.ReadFile("config.yaml")
	handler := func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
		rw.Write(testConfig)
	}
	server := httptest.NewServer(http.HandlerFunc(handler))
	defer server.Close()

	provider := httpprovider.NewHttpProvider(server.URL, []string{}, 2*time.Second, 2*time.Second)

	go provider.Provide()
	configs := <-provider.ConfigChan

	assert.Equal(len(configs), 1)
	assert.Equal(configs[0].Name, server.URL)
	assert.Equal(configs[0].FileBytes, string(testConfig))

	provider.Stop()
}

func TestProvideWithMultipleEndpoints(t *testing.T) {
	assert := assert.New(t)

	testConfig, _ := os.ReadFile("config.yaml")
	testExampleConfig, _ := os.ReadFile("example.yaml")

	mux := http.NewServeMux()
	mux.HandleFunc("/config", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
		rw.Write(testConfig)
	})
	mux.HandleFunc("/example", func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusOK)
		rw.Write(testExampleConfig)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	provider := httpprovider.NewHttpProvider(server.URL, []string{"config", "example"}, 1*time.Second, 1*time.Second)

	go provider.Provide()
	configs := <-provider.ConfigChan

	assert.Equal(len(configs), 2)
	assert.Equal(configs[0].Name, server.URL+"/config")
	assert.Equal(configs[1].Name, server.URL+"/example")
	assert.Equal(configs[0].FileBytes, string(testConfig))
	assert.Equal(configs[1].FileBytes, string(testExampleConfig))

	provider.Stop()
}
