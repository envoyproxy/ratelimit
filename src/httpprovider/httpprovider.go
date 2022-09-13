package httpprovider

import (
	"fmt"
	"io"
	"net/http"
	"time"

	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/config"
)

type HttpProvider struct {
	Endpoint     string
	Subpath      []string
	PollInterval time.Duration
	PollTimeout  time.Duration
	ConfigChan   chan []config.RateLimitConfigToLoad
	httpClient   *http.Client
	StopChan     chan bool
}

func NewHttpProvider(endpoint string, subpath []string, pollInterval time.Duration, pollTimeout time.Duration) *HttpProvider {
	return &HttpProvider{
		Endpoint:     endpoint,
		Subpath:      subpath,
		PollInterval: pollInterval,
		ConfigChan:   make(chan []config.RateLimitConfigToLoad),
		httpClient: &http.Client{
			Timeout: pollTimeout,
		},
		StopChan: make(chan bool),
	}
}

func (p *HttpProvider) Provide() error {
	for {
		select {
		case <-p.StopChan:
			return nil
		default:
			configs, err := p.fetchConfigurationData()
			if err != nil {
				logger.Errorf("Failed to fetch the configuration: %+v", err)
				time.Sleep(p.PollInterval)
				continue
			}
			p.ConfigChan <- configs
			time.Sleep(p.PollInterval)
		}
	}
}

func (p *HttpProvider) Stop() {
	p.StopChan <- true
}

// fetchConfigurationData fetches the configuration data from the configured endpoint.
func (p *HttpProvider) fetchConfigurationData() ([]config.RateLimitConfigToLoad, error) {
	configs := []config.RateLimitConfigToLoad{}

	urls := []string{}
	if len(p.Subpath) == 0 {
		urls = append(urls, p.Endpoint)
	} else {
		for _, path := range p.Subpath {
			urls = append(urls, fmt.Sprintf("%s/%s", p.Endpoint, path))
		}
	}

	for _, url := range urls {
		res, err := p.httpClient.Get(url)
		if err != nil {
			return nil, err
		}

		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("received non-ok response code: %d from the endpoint: %s", res.StatusCode, url)
		}

		body, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}
		configs = append(configs, config.RateLimitConfigToLoad{url, string(body)})
	}
	return configs, nil
}
