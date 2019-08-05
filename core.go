package mnemosyne

import (
	"fmt"

	"git.cafebazaar.ir/bazaar/search/octopus/pkg/epimetheus"
	"github.com/spf13/viper"
)

type Mnemosyne struct {
	name       string
	cachLayers []*Cache
	watcher    *epimetheus.Epimetheus
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
		if layerName == "memory" {
			cachLayers[i] = NewCacheInMem(config.GetInt(keyPrefix+".max-memory"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		} else if layerName == "redis" {
			cachLayers[i] = NewCacheRedis(config.GetString(keyPrefix+".address"),
				config.GetInt(keyPrefix+".db"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		} else if layerName == "gaurdian" {
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
	}
}
