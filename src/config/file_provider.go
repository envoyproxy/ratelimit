package config

import (
	"path/filepath"
	"strings"

	"github.com/envoyproxy/ratelimit/src/settings"
	"github.com/envoyproxy/ratelimit/src/stats"
	"github.com/lyft/goruntime/loader"
	gostats "github.com/lyft/gostats"
	logger "github.com/sirupsen/logrus"
)

type FileProvider struct {
	settings              settings.Settings
	loader                RateLimitConfigLoader
	configUpdateEventChan chan *ConfigUpdateEvent
	runtime               loader.IFace
	runtimeUpdateEvent    chan int
	runtimeWatchRoot      bool
	rootStore             gostats.Store
	statsManager          stats.Manager
}

func (p *FileProvider) ConfigUpdateEvent() <-chan *ConfigUpdateEvent {
	return p.configUpdateEventChan
}

func (p *FileProvider) watch() {
	p.runtime.AddUpdateCallback(p.runtimeUpdateEvent)

	go func() {
		p.sendEvent()
		// No exit right now.
		for {
			logger.Debugf("waiting for runtime update")
			<-p.runtimeUpdateEvent
			logger.Debugf("got runtime update and reloading config")
			p.sendEvent()
		}
	}()
}

func (p *FileProvider) sendEvent() {
	defer func() {
		if e := recover(); e != nil {
			p.configUpdateEventChan <- &ConfigUpdateEvent{Err: e}
		}
	}()

	files := []RateLimitConfigToLoad{}
	snapshot := p.runtime.Snapshot()
	for _, key := range snapshot.Keys() {
		if p.runtimeWatchRoot && !strings.HasPrefix(key, "config.") {
			continue
		}

		files = append(files, RateLimitConfigToLoad{key, snapshot.Get(key)})
	}

	rlSettings := settings.NewSettings()
	newConfig := p.loader.Load(files, p.statsManager, rlSettings.MergeDomainConfigurations)

	p.configUpdateEventChan <- &ConfigUpdateEvent{Config: newConfig}
}

func (p *FileProvider) setupRuntime() {
	loaderOpts := make([]loader.Option, 0, 1)
	if p.settings.RuntimeIgnoreDotFiles {
		loaderOpts = append(loaderOpts, loader.IgnoreDotFiles)
	} else {
		loaderOpts = append(loaderOpts, loader.AllowDotFiles)
	}
	var err error
	if p.settings.RuntimeWatchRoot {
		p.runtime, err = loader.New2(
			p.settings.RuntimePath,
			p.settings.RuntimeSubdirectory,
			p.rootStore.ScopeWithTags("runtime", p.settings.ExtraTags),
			&loader.SymlinkRefresher{RuntimePath: p.settings.RuntimePath},
			loaderOpts...)
	} else {
		directoryRefresher := &loader.DirectoryRefresher{}
		// Adding loader.Remove to the default set of goruntime's FileSystemOps.
		directoryRefresher.WatchFileSystemOps(loader.Remove, loader.Write, loader.Create, loader.Chmod)

		p.runtime, err = loader.New2(
			filepath.Join(p.settings.RuntimePath, p.settings.RuntimeSubdirectory),
			"config",
			p.rootStore.ScopeWithTags("runtime", p.settings.ExtraTags),
			directoryRefresher,
			loaderOpts...)
	}

	if err != nil {
		panic(err)
	}
}

func NewFileProvider(settings settings.Settings, rootStore gostats.Store, statsManager stats.Manager) RateLimitConfigProvider {
	p := &FileProvider{
		settings:              settings,
		loader:                NewRateLimitConfigLoaderImpl(),
		configUpdateEventChan: make(chan *ConfigUpdateEvent),
		runtimeUpdateEvent:    make(chan int),
		runtimeWatchRoot:      settings.RuntimeWatchRoot,
		rootStore:             rootStore,
		statsManager:          statsManager,
	}
	p.setupRuntime()
	go p.watch()
	return p
}
