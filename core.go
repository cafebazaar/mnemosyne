package mnemosyne

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// Mnemosyne is the parent object which holds all cache instances
type Mnemosyne struct {
	instances map[string]*MnemosyneInstance
}

// MnemosyneInstance is an instance of a multi-layer cache
type MnemosyneInstance struct {
	name         string
	cacheLayers  []*cache
	cacheWatcher ICounter
	softTTL      time.Duration
}

// NewMnemosyne initializes the Mnemosyne object which holds all cache instances
func NewMnemosyne(config *viper.Viper, commTimer ITimer, cacheHitCounter ICounter) *Mnemosyne {
	if config == nil {
		logrus.Panicf("%w: nil config", ErrInvalidConfig)
	}

	if commTimer == nil {
		commTimer = NewDummyTimer()
	}
	if cacheHitCounter == nil {
		cacheHitCounter = NewDummyCounter()
	}

	cacheConfigs := config.GetStringMap("cache")
	if len(cacheConfigs) == 0 {
		logrus.Panicf("%w: no cache configurations found", ErrInvalidConfig)
	}

	caches := make(map[string]*MnemosyneInstance, len(cacheConfigs))

	for cacheName := range cacheConfigs {
		instance, err := newMnemosyneInstance(cacheName, config, commTimer, cacheHitCounter)
		if err != nil {
			logrus.WithError(err).
				WithField("cache", cacheName).
				Error("Failed to initialize cache instance")
			continue
		}
		if instance != nil {
			caches[cacheName] = instance
		}
	}

	if len(caches) == 0 {
		logrus.Panicf("%w: no valid cache instances created", ErrInvalidConfig)
	}

	return &Mnemosyne{
		instances: caches,
	}
}

// Select returns a cache instance selected by name
func (m *Mnemosyne) Select(cacheName string) *MnemosyneInstance {
	instance, exists := m.instances[cacheName]
	if !exists {
		logrus.Panicf("cache instance %q not found", cacheName)
	}
	return instance
}

func newMnemosyneInstance(name string, config *viper.Viper, commTimer ITimer, hitCounter ICounter) (*MnemosyneInstance, error) {
	configKeyPrefix := fmt.Sprintf("cache.%s", name)
	layerNames := config.GetStringSlice(configKeyPrefix + ".layers")

	if len(layerNames) == 0 {
		return nil, fmt.Errorf("%w: no layers configured for cache instance %q", ErrInvalidConfig, name)
	}

	cacheLayers := make([]*cache, 0, len(layerNames))

	for _, layerName := range layerNames {
		keyPrefix := fmt.Sprintf("%s.%s", configKeyPrefix, layerName)
		layerType := config.GetString(keyPrefix + ".type")

		layer, err := createCacheLayer(layerType, layerName, keyPrefix, config, commTimer)
		if err != nil {
			return nil, fmt.Errorf("failed to create cache layer %q: %w", layerName, err)
		}

		cacheLayers = append(cacheLayers, layer)
	}

	softTTL := config.GetDuration(configKeyPrefix + ".soft-ttl")
	if softTTL <= 0 {
		return nil, fmt.Errorf("%w: invalid soft-ttl for cache instance %q", ErrInvalidConfig, name)
	}

	return &MnemosyneInstance{
		name:         name,
		cacheLayers:  cacheLayers,
		cacheWatcher: hitCounter,
		softTTL:      softTTL,
	}, nil
}

func createCacheLayer(layerType, layerName, keyPrefix string, config *viper.Viper, commTimer ITimer) (*cache, error) {
	switch layerType {
	case "memory":
		return newCacheInMem(layerName, config.GetInt(keyPrefix+".max-memory"), config.GetDuration(keyPrefix+".ttl"), config.GetInt(keyPrefix+".amnesia"), config.GetBool(keyPrefix+".compression")), nil

	case "redis":
		return newCacheRedis(layerName, config.GetString(keyPrefix+".address"), config.GetInt(keyPrefix+".db"), config.GetDuration(keyPrefix+".ttl"), config.GetDuration(keyPrefix+".idle-timeout"), config.GetInt(keyPrefix+".amnesia"), config.GetBool(keyPrefix+".compression"), commTimer), nil

	case "guardian":
		return newCacheClusterRedis(layerName, config.GetString(keyPrefix+".address"), config.GetStringSlice(keyPrefix+".slaves"), config.GetInt(keyPrefix+".db"), config.GetDuration(keyPrefix+".ttl"), config.GetDuration(keyPrefix+".idle-timeout"), config.GetInt(keyPrefix+".amnesia"), config.GetBool(keyPrefix+".compression"), commTimer), nil

	case "tiny":
		return newCacheTiny(layerName, config.GetInt(keyPrefix+".amnesia"), config.GetBool(keyPrefix+".compression")), nil

	default:
		return nil, fmt.Errorf("unknown cache type %q", layerType)
	}
}

func (mn *MnemosyneInstance) get(ctx context.Context, key string) (*cachableRet, error) {
	for i, layer := range mn.cacheLayers {
		result, err := layer.withContext(ctx).get(key)
		if err == nil {
			mn.cacheWatcher.Inc(mn.name, fmt.Sprintf("layer%d", i))
			go mn.fillUpperLayers(ctx, key, result, i)
			return result, nil
		}
	}

	mn.cacheWatcher.Inc(mn.name, "miss")
	return nil, ErrNotFound
}

// Get retrieves the value for key
func (mn *MnemosyneInstance) Get(ctx context.Context, key string, ref interface{}) error {
	cachableObj, err := mn.get(ctx, key)
	if err != nil {
		return err
	}

	if cachableObj == nil || cachableObj.CachedObject == nil {
		return ErrNilCache
	}

	return json.Unmarshal(*cachableObj.CachedObject, ref)
}

// GetAndShouldUpdate retrieves the value for key and indicates if the soft-TTL has expired
func (mn *MnemosyneInstance) GetAndShouldUpdate(ctx context.Context, key string, ref interface{}) (bool, error) {
	cachableObj, err := mn.get(ctx, key)
	if errors.Is(err, redis.Nil) {
		return true, err
	} else if err != nil {
		return false, err
	}

	if cachableObj == nil || cachableObj.CachedObject == nil {
		return false, ErrNilCache
	}

	if err := json.Unmarshal(*cachableObj.CachedObject, ref); err != nil {
		return false, fmt.Errorf("failed to unmarshal cached object: %w", err)
	}

	dataAge := time.Since(cachableObj.Time)
	go mn.monitorDataHotness(dataAge)

	return dataAge > mn.softTTL, nil
}

// ShouldUpdate indicates if the soft-TTL of a key has expired
func (mn *MnemosyneInstance) ShouldUpdate(ctx context.Context, key string) (bool, error) {
	cachableObj, err := mn.get(ctx, key)
	if errors.Is(err, redis.Nil) {
		return true, err
	} else if err != nil {
		return false, err
	}

	if cachableObj == nil || cachableObj.CachedObject == nil {
		return false, ErrNilCache
	}

	return time.Since(cachableObj.Time) > mn.softTTL, nil
}

// Set sets the value for a key in all layers of the cache instance
func (mn *MnemosyneInstance) Set(ctx context.Context, key string, value interface{}) error {
	if value == nil {
		return ErrNilValue
	}

	toCache := cachable{
		CachedObject: value,
		Time:         time.Now(),
	}

	var errs []string
	for _, layer := range mn.cacheLayers {
		if err := layer.withContext(ctx).set(key, toCache); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", layer.layerName, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cache set errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// TTL returns the TTL of the first accessible data instance and its layer index
func (mn *MnemosyneInstance) TTL(key string) (int, time.Duration) {
	for i, layer := range mn.cacheLayers {
		if dur := layer.getTTL(key); dur > 0 {
			return i, dur
		}
	}
	return -1, 0
}

// Delete removes a key from all layers
func (mn *MnemosyneInstance) Delete(ctx context.Context, key string) error {
	var errs []string
	for _, layer := range mn.cacheLayers {
		if err := layer.delete(ctx, key); err != nil && !errors.Is(err, redis.Nil) {
			errs = append(errs, fmt.Sprintf("%s: %v", layer.layerName, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cache delete errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Flush completely clears a single layer of the cache
func (mn *MnemosyneInstance) Flush(targetLayerName string) error {
	for _, layer := range mn.cacheLayers {
		if layer.layerName == targetLayerName {
			return layer.clear()
		}
	}
	return fmt.Errorf("%w: %s", ErrLayerNotFound, targetLayerName)
}

func (mn *MnemosyneInstance) fillUpperLayers(_ context.Context, key string, value *cachableRet, layer int) {
	if value == nil {
		return
	}

	for i := layer - 1; i >= 0; i-- {
		if err := mn.cacheLayers[i].set(key, *value); err != nil {
			logrus.WithError(err).
				WithField("layer", i).
				WithField("key", key).
				Errorf("failed to fill layer %d : %v", i, err)
		}
	}
}

func (mn *MnemosyneInstance) monitorDataHotness(age time.Duration) {
	switch {
	case age <= mn.softTTL:
		mn.cacheWatcher.Inc(mn.name+"-hotness", "hot")
	case age <= mn.softTTL*2:
		mn.cacheWatcher.Inc(mn.name+"-hotness", "warm")
	default:
		mn.cacheWatcher.Inc(mn.name+"-hotness", "cold")
	}
}
