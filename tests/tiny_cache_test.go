package tests

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cafebazaar/mnemosyne"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestTinyCacheConcurrentFlush(t *testing.T) {
	config := viper.New()
	config.Set("cache.result.soft-ttl", time.Hour)
	config.Set("cache.result.layers", []string{"tiny"})
	config.Set("cache.result.tiny.type", "tiny")
	cache := mnemosyne.NewMnemosyne(config, nil, nil).Select("result")
	ctx := context.Background()

	operations := []func() error{
		func() error { return cache.Set(ctx, "key", "value") },
		func() error {
			var value string
			err := cache.Get(ctx, "key", &value)
			if errors.Is(err, mnemosyne.ErrNotFound) {
				return nil
			}
			return err
		},
		func() error { return cache.Delete(ctx, "key") },
		func() error { return cache.Flush("tiny") },
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, operation := range operations {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for range 1000 {
				if err := operation(); err != nil {
					t.Errorf("concurrent cache operation failed: %v", err)
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()

	require.NoError(t, cache.Set(ctx, "first", "value"))
	require.NoError(t, cache.Set(ctx, "second", "value"))
	require.NoError(t, cache.Flush("tiny"))
	for _, key := range []string{"first", "second"} {
		var value string
		require.ErrorIs(t, cache.Get(ctx, key, &value), mnemosyne.ErrNotFound)
	}

	require.NoError(t, cache.Set(ctx, "after", "new value"))
	var value string
	require.NoError(t, cache.Get(ctx, "after", &value))
	require.Equal(t, "new value", value)
}
