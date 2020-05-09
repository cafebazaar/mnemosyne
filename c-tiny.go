package mnemosyne

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"
)

type tinyCache struct {
	layerName          string
	base               *sync.Map
	amnesiaChance      int
	compressionEnabled bool
	cacheTTL           time.Duration
	watcher            ITimer
}

func NewTinyCache(opts *CacheOpts, watcher ITimer) *tinyCache {
	data := sync.Map{}
	return &tinyCache{
		layerName:          opts.layerName,
		base:               &data,
		amnesiaChance:      opts.amnesiaChance,
		compressionEnabled: opts.compressionEnabled,
		cacheTTL:           time.Hour * 9999,
		watcher:            watcher,
	}
}

func (tc *tinyCache) Get(ctx context.Context, key string) (*cachableRet, error) {
	if tc.amnesiaChance > rand.Intn(100) {
		return nil, NewAmnesiaError(tc.amnesiaChance)
	}
	var rawBytes []byte
	val, ok := tc.base.Load(key)
	if !ok {
		return nil, errors.New("Failed to load from syncmap")
	} else {
		rawBytes, ok = val.([]byte)
		if !ok {
			return nil, errors.New("Failed to load from syncmap")
		}
	}
	return finalizeCacheResponse(rawBytes, tc.compressionEnabled)
}

func (tc *tinyCache) Set(ctx context.Context, key string, value interface{}) error {
	if tc.amnesiaChance == 100 {
		return NewAmnesiaError(tc.amnesiaChance)
	}
	finalData, err := prepareCachePayload(value, tc.compressionEnabled)
	if err != nil {
		return err
	}
	tc.base.Store(key, finalData)
	return nil
}

func (tc *tinyCache) Delete(ctx context.Context, key string) error {
	if tc.amnesiaChance == 100 {
		return NewAmnesiaError(tc.amnesiaChance)
	}
	tc.base.Delete(key)
	return nil
}

func (tc *tinyCache) Clear() error {
	tc.base = &sync.Map{}
	return nil
}

func (tc *tinyCache) TTL(ctx context.Context, key string) time.Duration {
	return time.Second * 0
}

func (tc *tinyCache) Name() string {
	return tc.layerName
}
