package mnemosyne

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/go-redis/redis"
	"github.com/pkg/errors"

	"git.cafebazaar.ir/bazaar/search/epimetheus.git"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// Mnemosyne is the parent object which holds all cache instances
type Mnemosyne struct {
	childs map[string]*MnemosyneInstance
}

// MnemosyneInstance is an instance of a multi-layer cache
type MnemosyneInstance struct {
	name        string
	cacheLayers []*cache
	watcher     *epimetheus.Epimetheus
	softTTL     time.Duration
}

// ErrCacheMiss is the Error returned when a cache miss happens
type ErrCacheMiss struct {
	message string
}

func (e *ErrCacheMiss) Error() string {
	return e.message
}

// NewMnemosyne initializes the Mnemosyne object which holds all the cache instances
func NewMnemosyne(config *viper.Viper, watcher *epimetheus.Epimetheus) *Mnemosyne {
	cacheConfigs := config.GetStringMap("cache")
	caches := make(map[string]*MnemosyneInstance, len(cacheConfigs))
	for cacheName := range cacheConfigs {
		caches[cacheName] = newMnemosyneInstance(cacheName, config, watcher)
	}
	return &Mnemosyne{
		childs: caches,
	}
}

// Select returns a cache instance selected by name
func (m *Mnemosyne) Select(cacheName string) *MnemosyneInstance {
	return m.childs[cacheName]
}

func newMnemosyneInstance(name string, config *viper.Viper, watcher *epimetheus.Epimetheus) *MnemosyneInstance {
	if watcher == nil {
		logrus.Fatal("Epimetheus Watcher should be given to Mnemosyne")
	}
	commTimer := watcher.CommTimer
	configKeyPrefix := fmt.Sprintf("cache.%s", name)
	layerNames := config.GetStringSlice(configKeyPrefix + ".layers")
	cacheLayers := make([]*cache, len(layerNames))
	for i, layerName := range layerNames {
		keyPrefix := configKeyPrefix + "." + layerName
		layerType := config.GetString(keyPrefix + ".type")
		if layerType == "memory" {
			cacheLayers[i] = newCacheInMem(
				layerName,
				config.GetInt(keyPrefix+".max-memory"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
			)
		} else if layerType == "redis" {
			cacheLayers[i] = newCacheRedis(
				layerName,
				config.GetString(keyPrefix+".address"),
				config.GetInt(keyPrefix+".db"),
				config.GetDuration(keyPrefix+".ttl"),
				config.GetDuration(keyPrefix+".idle-timeout"),
				config.GetInt(keyPrefix+".amnesia"),
				config.GetBool(keyPrefix+".compression"),
				commTimer,
			)
		} else if layerType == "gaurdian" {
			cacheLayers[i] = newCacheClusterRedis(
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
			cacheLayers[i] = newCacheTiny(
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
		result, cacheErrors[i] = layer.withContext(ctx).get(key)
		if cacheErrors[i] == nil {
			go mn.watcher.CacheRate.Inc(mn.name, fmt.Sprintf("layer%d", i))
			go mn.fillUpperLayers(key, result, i)
			return result, nil
		}
	}
	go mn.watcher.CacheRate.Inc(mn.name, "miss")
	return nil, &ErrCacheMiss{message: "Miss"} // FIXME: better Error combination
}

// Get retrieves the value for key
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

// GetAndShouldUpdate retrieves the value for key and also shows whether the soft-TTL of that key has passed or not
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

// ShouldUpdate shows whether the soft-TTL of a key has passed or not
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

// Set sets the value for a key in all layers of the cache instance
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
		cacheErrors[i] = layer.withContext(ctx).set(key, toCache)
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

// TTL returns the TTL of the first accessible data instance as well as the layer it was found on
func (mn *MnemosyneInstance) TTL(key string) (int, time.Duration) {
	for i, layer := range mn.cacheLayers {
		dur := layer.getTTL(key)
		if dur > 0 {
			return i, dur
		}
	}
	return -1, time.Second * 0
}

// Delete removes a key from all the layers (if exists)
func (mn *MnemosyneInstance) Delete(ctx context.Context, key string) error {
	cacheErrors := make([]error, len(mn.cacheLayers))
	errorStrings := make([]string, len(mn.cacheLayers))
	haveErorr := false
	for i, layer := range mn.cacheLayers {
		cacheErrors[i] = layer.delete(ctx, key)
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

// Flush completly clears a single layer of the cache
func (mn *MnemosyneInstance) Flush(targetLayerName string) error {
	for _, layer := range mn.cacheLayers {
		if layer.layerName == targetLayerName {
			return layer.clear()
		}
	}
	return fmt.Errorf("Layer Named: %v Not Found", targetLayerName)
}

func (mn *MnemosyneInstance) fillUpperLayers(key string, value *cachableRet, layer int) {
	for i := layer - 1; i >= 0; i-- {
		if value == nil {
			continue
		}
		err := mn.cacheLayers[i].set(key, *value)
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
