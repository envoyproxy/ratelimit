package provider

import (
	"crypto/x509"
	"path/filepath"
	"sync"

	"github.com/lyft/goruntime/loader"
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/utils"
)

// ClientCAProvider will watch caDirectory for changes via goruntime/loader and reload the client CA file
type ClientCAProvider struct {
	settings           settings.Settings
	runtime            loader.IFace
	runtimeUpdateEvent chan int
	rootStore          gostats.Store
	poolLock           sync.RWMutex
	pool               *x509.CertPool
	caDirectory        string
	caFile             string
}

// ClientCAs returns the current client CA pool
func (p *ClientCAProvider) ClientCAs() *x509.CertPool {
	p.poolLock.RLock()
	defer p.poolLock.RUnlock()
	return p.pool
}

func (p *ClientCAProvider) watch() {
	p.runtime.AddUpdateCallback(p.runtimeUpdateEvent)

	go func() {
		for {
			logger.Debugf("ClientCAProvider: waiting for runtime update")
			<-p.runtimeUpdateEvent
			logger.Debugf("ClientCAProvider: got runtime update and reloading client CA")
			p.reloadClientCAs()
		}
	}()
}

// reloadClientCAs loads the client CA file and updates the x509.CertPool in memory
func (p *ClientCAProvider) reloadClientCAs() {
	pool, err := utils.LoadCertPool(p.caFile)
	if err != nil {
		logger.Errorf("ClientCAProvider failed to load client CA (%s): %v", p.caFile, err)
		// panic in case there is no client CA already loaded as this would mean starting up without client verification
		if p.ClientCAs() == nil {
			logger.Fatalf("ClientCAProvider failed to load any client CA, exiting.")
		}
		return // keep the old client CA if we have one
	}
	p.poolLock.Lock()
	defer p.poolLock.Unlock()
	p.pool = pool
	logger.Infof("ClientCAProvider reloaded client CA from %s", p.caFile)
}

// setupRuntime sets up the goruntime loader to watch the caDirectory
// Will panic if it fails to set up the loader
func (p *ClientCAProvider) setupRuntime() {
	var err error

	// runtimePath is the parent folder of caDirectory
	runtimePath := filepath.Dir(p.caDirectory)
	// runtimeSubdirectory is the name of the folder to watch, containing the client CA
	runtimeSubdirectory := filepath.Base(p.caDirectory)

	p.runtime, err = loader.New2(
		runtimePath,
		runtimeSubdirectory,
		p.rootStore.ScopeWithTags("client_ca", p.settings.ExtraTags),
		&loader.DirectoryRefresher{},
		loader.IgnoreDotFiles)
	if err != nil {
		logger.Fatalf("Failed to set up goruntime loader: %v", err)
	}
}

// NewClientCAProvider creates a new ClientCAProvider
// Will panic if it fails to set up goruntime or fails to load the initial client CA
func NewClientCAProvider(settings settings.Settings, rootStore gostats.Store, caFile string) *ClientCAProvider {
	p := &ClientCAProvider{
		settings:           settings,
		runtimeUpdateEvent: make(chan int),
		rootStore:          rootStore,
		caDirectory:        filepath.Dir(caFile),
		caFile:             caFile,
	}
	p.setupRuntime()
	// Initially load the client CA (or panic)
	p.reloadClientCAs()
	go p.watch()
	return p
}
