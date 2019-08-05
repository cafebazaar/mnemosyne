package mnemosyne

import (
	"context"
	"fmt"

	"git.cafebazaar.ir/bazaar/search/octopus/pkg/epimetheus"
	"github.com/spf13/viper"
)

type Mnemosyne struct {
	name       string
	cachLayers []*Cache
	watcher    *epimetheus.Epimetheus
	ctx        *context.Context
}

func NewMnemosyne(name string, config *viper.Viper, watcher *epimetheus.Epimetheus) *Mnemosyne {
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
	return &Mnemosyne{
		name:       name,
		cachLayers: cachLayers,
		watcher:    watcher,
		ctx:		context.Background(),
	}
}

func (mn *Mnemosyne) WithContext(ctx *context.Context) *Mnemosyne{
	return &Mnemosyne{
		name:       mn.name,
		cachLayers: mn.cachLayers,
		watcher:    mn.watcher,
		ctx:		ctx,
	}
}

func (mn *Mnemosyne) Get(key string) interface{} {
	cacheErrors := make([]error, len(mn.cacheLayers))
	var result interface{}
	for i, layer := range mn.cachLayers{
		result, cacheErrors[i] = layer.Get(key)
		if err==nil	{
			go mn.watcher.CacheRate.Inc(mn.name, Sprintf("layer%d", i))
			return result
		}
	}
	go mn.watcher.CacheRate.Inc(mn.name, "miss")
	return nil
}

func (mn *Mnemosyne) Set(key string, value interface{}) error {
	cacheErrors := make([]error, len(mn.cacheLayers))
	for i, layer := range mn.cachLayers{
		cacheErrors[i] = layer.Set(key, value)
	}
	return nil
}

func (mn *Mnemosyne) TTL(key string) (int, time.Duration) {
	for i, layer := range mn.cachLayers{
		dur := layer.GetTTL(key)
		if dur > 0 {
			return i, dur
		}
	}
	return -1, time.second * 0
}