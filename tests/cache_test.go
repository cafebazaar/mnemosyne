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

func TestScanReturnsMatchingKeys(t *testing.T) {
	cacheInstance := setupTestCache(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	keys := map[string]TestType{
		"result;alpha;1": {Name: "a1"},
		"result;alpha;2": {Name: "a2"},
		"result;beta;1":  {Name: "b1"},
	}

	for key, value := range keys {
		err := cacheInstance.Set(ctx, key, value)
		assert.NoError(t, err, "Failed to set cache item %s", key)
	}

	matchedKeys, err := cacheInstance.Scan(ctx, "*;alpha;*", 100)
	assert.NoError(t, err, "Failed to scan matching keys")
	assert.ElementsMatch(t, []string{"result;alpha;1", "result;alpha;2"}, matchedKeys)
}

func TestDeleteManyDeletesSpecifiedKeys(t *testing.T) {
	cacheInstance := setupTestCache(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cacheInstance.Set(ctx, "result;one;key", TestType{Name: "one"})
	assert.NoError(t, err)
	err = cacheInstance.Set(ctx, "result;two;key", TestType{Name: "two"})
	assert.NoError(t, err)
	err = cacheInstance.Set(ctx, "result;three;key", TestType{Name: "three"})
	assert.NoError(t, err)

	err = cacheInstance.DeleteMany(ctx, "result;one;key", "result;two;key")
	assert.NoError(t, err, "Failed to delete many keys")

	var missing TestType
	err = cacheInstance.Get(ctx, "result;one;key", &missing)
	assert.ErrorIs(t, err, mnemosyne.ErrNotFound)
	err = cacheInstance.Get(ctx, "result;two;key", &missing)
	assert.ErrorIs(t, err, mnemosyne.ErrNotFound)

	var remaining TestType
	err = cacheInstance.Get(ctx, "result;three;key", &remaining)
	assert.NoError(t, err, "Remaining key should still exist")
	assert.Equal(t, TestType{Name: "three"}, remaining)
}

func TestDeleteByPatternDeletesMatchingKeys(t *testing.T) {
	cacheInstance := setupTestCache(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cacheInstance.Set(ctx, "result;delete;1", TestType{Name: "d1"})
	assert.NoError(t, err)
	err = cacheInstance.Set(ctx, "result;delete;2", TestType{Name: "d2"})
	assert.NoError(t, err)
	err = cacheInstance.Set(ctx, "result;keep;1", TestType{Name: "k1"})
	assert.NoError(t, err)

	deletedCount, err := cacheInstance.DeleteByPattern(ctx, "*;delete;*", 100)
	assert.NoError(t, err, "Failed to delete by pattern")
	assert.Equal(t, 2, deletedCount)

	var deleted TestType
	err = cacheInstance.Get(ctx, "result;delete;1", &deleted)
	assert.ErrorIs(t, err, mnemosyne.ErrNotFound)
	err = cacheInstance.Get(ctx, "result;delete;2", &deleted)
	assert.ErrorIs(t, err, mnemosyne.ErrNotFound)

	var kept TestType
	err = cacheInstance.Get(ctx, "result;keep;1", &kept)
	assert.NoError(t, err, "Non-matching key should remain")
	assert.Equal(t, TestType{Name: "k1"}, kept)
}

func TestDeleteByPatternNoMatches(t *testing.T) {
	cacheInstance := setupTestCache(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cacheInstance.Set(ctx, "result;keep;key", TestType{Name: "keep"})
	assert.NoError(t, err)

	deletedCount, err := cacheInstance.DeleteByPattern(ctx, "*;missing;*", 100)
	assert.NoError(t, err, "DeleteByPattern should not fail when nothing matches")
	assert.Equal(t, 0, deletedCount)

	var kept TestType
	err = cacheInstance.Get(ctx, "result;keep;key", &kept)
	assert.NoError(t, err, "Key should still exist")
	assert.Equal(t, TestType{Name: "keep"}, kept)
}

func TestDeleteManyWithNoKeys(t *testing.T) {
	cacheInstance := setupTestCache(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := cacheInstance.DeleteMany(ctx)
	assert.NoError(t, err, "DeleteMany should allow empty input")
}
