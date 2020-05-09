package tests

import (
	"context"
	"testing"
	"time"

	"bou.ke/monkey"
	"github.com/alicebob/miniredis"
	"github.com/cafebazaar/mnemosyne"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func NewConfig() *viper.Viper {
	config := viper.New()
	config.SetConfigName("mock_config")
	config.AddConfigPath(".")
	_ = config.ReadInConfig()
	return config
}

type TestType struct {
	Name string
}

func newTestRedis() string {
	mr, err := miniredis.Run()
	if err != nil {
		panic(err)
	}
	return mr.Addr()
}

func setUp() *mnemosyne.MnemosyneInstance {
	config := NewConfig()
	addr := newTestRedis()
	config.SetDefault("cache.result.user-redis.address", addr)
	mnemosyneManager := mnemosyne.NewMnemosyne(config, nil, nil)
	cacheInstance := mnemosyneManager.Select("result")
	return cacheInstance
}
func TestGetAndShouldUpdate(t *testing.T) {
	cacheInstance := setUp()

	cacheCtx, cacheCancelFunc := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cacheCancelFunc()

	testCache := TestType{Name: "test me"}
	cacheInstance.Set(cacheCtx, "test_item1", testCache)
	var myCachedData TestType

	shouldUpdate, err := cacheInstance.GetAndShouldUpdate(cacheCtx, "test_item1", &myCachedData)

	assert.Equal(t, testCache, myCachedData)

	assert.Equal(t, nil, err)

	assert.Equal(t, false, shouldUpdate)

	wayback := time.Now().Add(time.Hour * 3)
	patch := monkey.Patch(time.Now, func() time.Time { return wayback })
	defer patch.Unpatch()

	shouldUpdate, _ = cacheInstance.GetAndShouldUpdate(cacheCtx, "test_item1", &myCachedData)

	assert.Equal(t, true, shouldUpdate)
}
