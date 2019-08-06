package mnemosyne

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.cafebazaar.ir/bazaar/search/octopus/pkg/epimetheus"
	"github.com/spf13/viper"
)

type Mnemosyne struct {
	childs map[string]*MnemosyneInstance
}

type MnemosyneInstance struct {
	name        string
	cacheLayers []*Cache
	watcher     *epimetheus.Epimetheus
	ctx         context.Context
}

func NewMnemosyne(config *viper.Viper, watcher *epimetheus.Epimetheus) *Mnemosyne {
	cacheConfigs := viper.GetStringMap("cache")
	caches := make(map[string]*MnemosyneInstance, len(cacheConfigs))
	for cacheName := range cacheConfigs {
		caches[cacheName] = NewMnemosyneInstance(cacheName, config, watcher)
	}
	return &Mnemosyne{
		childs: caches,
	}
}

func (m *Mnemosyne) Select(cacheName string) *MnemosyneInstance {
	return m.childs[cacheName]
}

func NewMnemosyneInstance(name string, config *viper.Viper, watcher *epimetheus.Epimetheus) *MnemosyneInstance {
	if watcher == nil {
		watcher = epimetheus.NewEpimetheus(config)
	}
	configKeyPrefix := fmt.Sprintf("cache.%s", name)
	layerNames := config.GetStringSlice(configKeyPrefix + ".layers")
	cachLayers := make([]*Cache, len(layerNames))
	for i, layerName := range layerNames {
		keyPrefix := configKeyPrefix + "." + layerName
		layerType := config.GetString(keyPrefix + ".type")
		if layerType == "memory" {
			cachLayers[i] = NewCacheInMem(config.GetInt(keyPrefix+".max-memory"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		} else if layerType == "redis" {
			cachLayers[i] = NewCacheRedis(config.GetString(keyPrefix+".address"),
				config.GetInt(keyPrefix+".db"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		} else if layerType == "gaurdian" {
			cachLayers[i] = NewCacheClusterRedis(config.GetString(keyPrefix+".address"),
				config.GetStringSlice(keyPrefix+".slaves"),
				config.GetInt(keyPrefix+".db"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		}
	}
	return &MnemosyneInstance{
		name:        name,
		cacheLayers: cachLayers,
		watcher:     watcher,
		ctx:         context.Background(),
	}
}

func (mn *MnemosyneInstance) WithContext(ctx context.Context) *MnemosyneInstance {
	return &MnemosyneInstance{
		name:        mn.name,
		cacheLayers: mn.cacheLayers,
		watcher:     mn.watcher,
		ctx:         ctx,
	}
}

func (mn *MnemosyneInstance) Get(key string) (interface{}, error) {
	cacheErrors := make([]error, len(mn.cacheLayers))
	var result interface{}
	for i, layer := range mn.cacheLayers {
		result, cacheErrors[i] = layer.Get(key)
		if cacheErrors[i] == nil {
			go mn.watcher.CacheRate.Inc(mn.name, fmt.Sprintf("layer%d", i))
			return result, nil
		}
	}
	go mn.watcher.CacheRate.Inc(mn.name, "miss")
	return nil, errors.New("Miss") // FIXME: better Error combination
}

func (mn *MnemosyneInstance) Set(key string, value interface{}) error {
	cacheErrors := make([]error, len(mn.cacheLayers))
	for i, layer := range mn.cacheLayers {
		cacheErrors[i] = layer.Set(key, value)
	}
	return nil // FIXME: better Error combination
}

func (mn *MnemosyneInstance) TTL(key string) (int, time.Duration) {
	for i, layer := range mn.cacheLayers {
		dur := layer.GetTTL(key)
		if dur > 0 {
			return i, dur
		}
	}
	return -1, time.Second * 0
}
