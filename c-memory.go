package mnemosyne

import (
	"context"
	"math/rand"
	"time"

	"github.com/allegro/bigcache"
	"github.com/sirupsen/logrus"
)

type inMemoryCache struct {
	baseCache
	base     *bigcache.BigCache
	cacheTTL time.Duration
	watcher  ITimer
}

func NewInMemoryCache(opts *CacheOpts, watcher ITimer) *inMemoryCache {
	internalOpts := bigcache.Config{
		Shards:             1024,
		LifeWindow:         opts.cacheTTL,
		MaxEntriesInWindow: 1100 * 10 * 60,
		MaxEntrySize:       500,
		Verbose:            false,
		HardMaxCacheSize:   opts.memOpts.maxMem,
		CleanWindow:        1 * time.Minute,
	}
	cacheInstance, err := bigcache.NewBigCache(internalOpts)
	if err != nil {
		logrus.Errorf("InMemCache Error: %v", err)
	}
	return &inMemoryCache{
		baseCache: baseCache{
			layerName:          opts.layerName,
			amnesiaChance:      opts.amnesiaChance,
			compressionEnabled: opts.compressionEnabled,
		},
		base:     cacheInstance,
		cacheTTL: opts.cacheTTL,
		watcher:  watcher,
	}
}

func (mc *inMemoryCache) Get(ctx context.Context, key string) (*cachableRet, error) {
	if mc.amnesiaChance > rand.Intn(100) {
		return nil, newAmnesiaError(mc.amnesiaChance)
	}
	rawBytes, err := mc.base.Get(key)
	if err != nil {
		return nil, err
	}
	return finalizeCacheResponse(rawBytes, mc.compressionEnabled)
}

func (mc *inMemoryCache) Set(ctx context.Context, key string, value interface{}) error {
	finalData, err := prepareCachePayload(value, mc.compressionEnabled)
	if err != nil {
		return err
	}
	return mc.base.Set(key, finalData)
}

func (mc *inMemoryCache) Delete(ctx context.Context, key string) error {
	return mc.base.Delete(key)
}

func (mc *inMemoryCache) Clear() error {
	return mc.base.Reset()
}

func (mc *inMemoryCache) TTL(ctx context.Context, key string) time.Duration {
	return time.Second * 0
}

func (mc *inMemoryCache) Name() string {
	return mc.layerName
}
