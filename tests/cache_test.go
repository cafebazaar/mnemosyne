package tests

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/cafebazaar/mnemosyne"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// TestType represents a sample struct for testing cache operations
type TestType struct {
	Name string
}

// testRedisServer creates and returns a new miniredis server instance
func testRedisServer(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	mr := miniredis.RunT(t)
	t.Cleanup(func() {
		mr.Close()
	})
	return mr
}

// setupTestCache creates a new mnemosyne cache instance for testing
func setupTestCache(t *testing.T) *mnemosyne.MnemosyneInstance {
	t.Helper()
	config := newTestConfig()
	mr := testRedisServer(t)
	config.Set("cache.result.user-redis.address", mr.Addr())

	mnemosyneManager := mnemosyne.NewMnemosyne(config, nil, nil)
	return mnemosyneManager.Select("result")
}

// newTestConfig creates a new Viper configuration for testing
func newTestConfig() *viper.Viper {
	config := viper.New()
	config.SetConfigName("mock_config")
	config.AddConfigPath(".")
	if err := config.ReadInConfig(); err != nil {
		// Log error or handle it appropriately
		config.SetDefault("cache.result.user-redis.address", "localhost:6379")
	}
	return config
}

func TestGetAndShouldUpdate(t *testing.T) {
	cacheInstance := setupTestCache(t)

	// Use a reasonable timeout for the context
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Test data
	testCache := TestType{Name: "test me"}

	// Test setting and getting cache
	err := cacheInstance.Set(ctx, "test_item1", testCache)
	assert.NoError(t, err, "Failed to set cache")

	var cachedData TestType
	shouldUpdate, err := cacheInstance.GetAndShouldUpdate(ctx, "test_item1", &cachedData)

	assert.NoError(t, err, "Failed to get cache")
	assert.Equal(t, testCache, cachedData, "Cached data does not match original")
	assert.False(t, shouldUpdate, "Should not update immediately after setting")
}
