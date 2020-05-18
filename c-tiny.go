package mnemosyne

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"
)

type tinyCache struct {
	baseCache
	base *sync.Map
}

func NewTinyCache(opts *CacheOpts) *tinyCache {
	data := sync.Map{}
	return &tinyCache{
		baseCache: baseCache{
			layerName:          opts.layerName,
			amnesiaChance:      opts.amnesiaChance,
			compressionEnabled: opts.compressionEnabled,
		},
		base: &data,
	}
}

func (tc *tinyCache) Get(ctx context.Context, key string) (*cachableRet, error) {
	if tc.amnesiaChance > rand.Intn(100) {
		return nil, newAmnesiaError(tc.amnesiaChance)
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
	finalData, err := prepareCachePayload(value, tc.compressionEnabled)
	if err != nil {
		return err
	}
	tc.base.Store(key, finalData)
	return nil
}

func (tc *tinyCache) Delete(ctx context.Context, key string) error {
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
