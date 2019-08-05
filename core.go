package mnemosyne

import (
	"context"
	"fmt"

	"git.cafebazaar.ir/bazaar/search/octopus/pkg/epimetheus"
	"github.com/spf13/viper"
)

type Mnemosyne struct {
	childs	[]*MnemosyneInstance
}


type MnemosyneInstance struct {
	name       string
	cachLayers []*Cache
	watcher    *epimetheus.Epimetheus
	ctx        *context.Context
}

func NewMnemosyne(config *viper.Viper, watcher *epimetheus.Epimetheus) *Mnemosyne{
	cacheConfigs := viper.GetStringMap("cache")
	caches := make([]*Mnemosyne, len(cacheConfigs))
	i := 0
	for cacheName :=range cacheConfigs{
		caches[i] = NewMnemosyneInstance(cacheName, config, watcher)
	}
	return &Mnemosyne{
		childs: caches,
	}
}

func (m *Mnemosyn) Select(cacheName string) *MnemosyneInstance{
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
		name:       name,
		cachLayers: cachLayers,
		watcher:    watcher,
		ctx:		context.Background(),
	}
}

func (mn *MnemosyneInstance) WithContext(ctx *context.Context) *MnemosyneInstance{
	return &MnemosyneInstance{
		name:       mn.name,
		cachLayers: mn.cachLayers,
		watcher:    mn.watcher,
		ctx:		ctx,
	}
}

func (mn *MnemosyneInstance) Get(key string) (interface{}, error){
	cacheErrors := make([]error, len(mn.cacheLayers))
	var result interface{}
	for i, layer := range mn.cachLayers{
		result, cacheErrors[i] = layer.Get(key)
		if err==nil	{
			go mn.watcher.CacheRate.Inc(mn.name, Sprintf("layer%d", i))
			return result, nil
		}
	}
	go mn.watcher.CacheRate.Inc(mn.name, "miss")
	return nil, Errors.New("Miss") // FIXME: better Error combination
}

func (mn *MnemosyneInstance) Set(key string, value interface{}) error {
	cacheErrors := make([]error, len(mn.cacheLayers))
	for i, layer := range mn.cachLayers{
		cacheErrors[i] = layer.Set(key, value)
	}
	return nil // FIXME: better Error combination
}

func (mn *MnemosyneInstance) TTL(key string) (int, time.Duration) {
	for i, layer := range mn.cachLayers{
		dur := layer.GetTTL(key)
		if dur > 0 {
			return i, dur
		}
	}
	return -1, time.second * 0
}