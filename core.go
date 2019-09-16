package mnemosyne

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/pkg/errors"

	"git.cafebazaar.ir/bazaar/search/octopus/pkg/epimetheus"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Mnemosyne struct {
	childs map[string]*MnemosyneInstance
}

type MnemosyneInstance struct {
	name        string
	cacheLayers []*Cache
	watcher     *epimetheus.Epimetheus
	softTTL     time.Duration
}

type ErrCacheMiss struct {
	message string
}

func (e *ErrCacheMiss) Error() string {
	return e.message
}

func NewMnemosyne(config *viper.Viper, watcher *epimetheus.Epimetheus) *Mnemosyne {
	cacheConfigs := config.GetStringMap("cache")
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
		logrus.Fatal("Epimetheus Watcher should be given to Mnemosyne")
	}
	commTimer := watcher.CommTimer
	configKeyPrefix := fmt.Sprintf("cache.%s", name)
	layerNames := config.GetStringSlice(configKeyPrefix + ".layers")
	cacheLayers := make([]*Cache, len(layerNames))
	for i, layerName := range layerNames {
		keyPrefix := configKeyPrefix + "." + layerName
		layerType := config.GetString(keyPrefix + ".type")
		if layerType == "memory" {
			cacheLayers[i] = NewCacheInMem(
				layerName,
				config.GetInt(keyPrefix+".max-memory"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		} else if layerType == "redis" {
			cacheLayers[i] = NewCacheRedis(
				layerName,
				config.GetString(keyPrefix+".address"),
				config.GetInt(keyPrefix+".db"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
				commTimer,
			)
		} else if layerType == "gaurdian" {
			cacheLayers[i] = NewCacheClusterRedis(
				layerName,
				config.GetString(keyPrefix+".address"),
				config.GetStringSlice(keyPrefix+".slaves"),
				config.GetInt(keyPrefix+".db"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
				commTimer,
			)
		} else if layerType == "tiny" {
			cacheLayers[i] = NewCacheTiny(
				layerName,
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		} else {
			logrus.Error("Malformed Config: Unknown cache type %s", layerType)
			return nil
		}
	}
	return &MnemosyneInstance{
		name:        name,
		cacheLayers: cacheLayers,
		watcher:     watcher,
		softTTL:     config.GetDuration(configKeyPrefix + ".soft-ttl"),
	}
}

func (mn *MnemosyneInstance) get(ctx context.Context, key string) (*cachableRet, error) {
	cacheErrors := make([]error, len(mn.cacheLayers))
	var result *cachableRet
	for i, layer := range mn.cacheLayers {
		result, cacheErrors[i] = layer.WithContext(ctx).Get(key)
		if cacheErrors[i] == nil {
			go mn.watcher.CacheRate.Inc(mn.name, fmt.Sprintf("layer%d", i))
			go mn.fillUpperLayers(key, result, i)
			return result, nil
		}
	}
	go mn.watcher.CacheRate.Inc(mn.name, "miss")
	return nil, &ErrCacheMiss{message: "Miss"} // FIXME: better Error combination
}

func (mn *MnemosyneInstance) Get(ctx context.Context, key string, ref interface{}) error {
	cachableObj, err := mn.get(ctx, key)
	if err != nil {
		return err
	}

	if cachableObj == nil || cachableObj.CachedObject == nil {
		logrus.Errorf("nil object found in cache %s ! %v", key, cachableObj)
		return errors.New("nil found")
	}

	err = json.Unmarshal(*cachableObj.CachedObject, ref)
	if err != nil {
		return err
	}
	return nil
}

func (mn *MnemosyneInstance) GetAndShouldUpdate(ctx context.Context, key string, ref interface{}) (bool, error) {
	cachableObj, err := mn.get(ctx, key)
	if err == redis.Nil {
		return true, err
	} else if err != nil {
		return false, err
	}

	if cachableObj == nil || cachableObj.CachedObject == nil {
		logrus.Errorf("nil object found in cache %s ! %v", key, cachableObj)
		return false, errors.New("nil found")
	}

	err = json.Unmarshal(*cachableObj.CachedObject, ref)
	if err != nil {
		return false, err
	}
	dataAge := time.Now().Sub(cachableObj.Time)
	go mn.monitorDataHotness(dataAge)
	shouldUpdate := dataAge > mn.softTTL
	return shouldUpdate, nil
}

func (mn *MnemosyneInstance) ShouldUpdate(ctx context.Context, key string) (bool, error) {
	cachableObj, err := mn.get(ctx, key)
	if err == redis.Nil {
		return true, err
	} else if err != nil {
		return false, err
	}

	if cachableObj == nil || cachableObj.CachedObject == nil {
		logrus.Errorf("nil object found in cache %s ! %v", key, cachableObj)
		return false, errors.New("nil found")
	}

	shouldUpdate := time.Now().Sub(cachableObj.Time) > mn.softTTL

	return shouldUpdate, nil
}

func (mn *MnemosyneInstance) Set(ctx context.Context, key string, value interface{}) error {
	if value == nil {
		return fmt.Errorf("cannot set nil value in cache")
	}

	toCache := cachable{
		CachedObject: value,
		Time:         time.Now(),
	}
	cacheErrors := make([]error, len(mn.cacheLayers))
	errorStrings := make([]string, len(mn.cacheLayers))
	haveErorr := false
	for i, layer := range mn.cacheLayers {
		cacheErrors[i] = layer.WithContext(ctx).Set(key, toCache)
		if cacheErrors[i] != nil {
			errorStrings[i] = cacheErrors[i].Error()
			haveErorr = true
		}
	}
	if haveErorr {
		return fmt.Errorf(strings.Join(errorStrings, ";"))
	}
	return nil
}

func (mn *MnemosyneInstance) TTL(key string) (int, time.Duration) {
	for i, layer := range mn.cacheLayers {
		dur := layer.TTL(key)
		if dur > 0 {
			return i, dur
		}
	}
	return -1, time.Second * 0
}

func (mn *MnemosyneInstance) fillUpperLayers(key string, value *cachableRet, layer int) {
	for i := layer - 1; i >= 0; i-- {
		if value == nil {
			continue
		}
		err := mn.cacheLayers[i].Set(key, *value)
		if err != nil {
			logrus.Errorf("failed to fill layer %d : %v", i, err)
		}
	}
}

func (mn *MnemosyneInstance) monitorDataHotness(age time.Duration) {
	if age <= mn.softTTL {
		mn.watcher.CacheRate.Inc(mn.name+"-hotness", "hot")
	} else if age <= mn.softTTL*2 {
		mn.watcher.CacheRate.Inc(mn.name+"-hotness", "warm")
	} else {
		mn.watcher.CacheRate.Inc(mn.name+"-hotness", "cold")
	}
}
