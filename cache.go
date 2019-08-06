package mnemosyne

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/allegro/bigcache"
	"github.com/go-redis/redis"
	"github.com/sirupsen/logrus"
	msgpack "gopkg.in/vmihailenco/msgpack.v2"
)

type Cache struct {
	baseRedisClient    *redis.Client
	slaveRedisClients  []*redis.Client
	inMemCache         *bigcache.BigCache
	amnesiaChance      int
	compressionEnabled bool
	TTL                time.Duration
	ctx                context.Context
}

func NewCacheRedis(addr string, db int, TTL time.Duration, amnesiaChance int, compressionEnabled bool) *Cache {
	redisOptions := &redis.Options{
		Addr: addr,
		DB:   db,
	}
	redisClient := redis.NewClient(redisOptions)

	err := redisClient.Ping().Err()
	if err != nil {
		logrus.WithError(err).Error("error while connecting to Redis")
	}
	return &Cache{
		baseRedisClient:    redisClient,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		TTL:                TTL,
		ctx:                context.Background(),
	}
}

func NewCacheClusterRedis(masterAddr string, slaveAddrs []string, db int, TTL time.Duration, amnesiaChance int, compressionEnabled bool) *Cache {
	slaveClients := make([]*redis.Client, len(slaveAddrs))
	for i, addr := range slaveAddrs {
		redisOptions := &redis.Options{
			Addr: addr,
			DB:   db,
		}
		slaveClients[i] = redis.NewClient(redisOptions)
	}

	redisOptions := &redis.Options{
		Addr: masterAddr,
		DB:   db,
	}
	redisClient := redis.NewClient(redisOptions)

	if err := redisClient.Ping().Err(); err != nil {
		logrus.WithError(err).Error("error while connecting to Redis Master")
	}

	return &Cache{
		baseRedisClient:    redisClient,
		slaveRedisClients:  slaveClients,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		TTL:                TTL,
		ctx:                context.Background(),
	}
}

func NewCacheInMem(maxMem int, TTL time.Duration, amnesiaChance int, compressionEnabled bool) *Cache {
	opts := bigcache.Config{
		Shards:             1024,
		LifeWindow:         TTL,
		MaxEntriesInWindow: 1100 * 10 * 60,
		MaxEntrySize:       500,
		Verbose:            false,
		HardMaxCacheSize:   maxMem,
	}
	cacheInstance, err := bigcache.NewBigCache(opts)
	if err != nil {
		logrus.Errorf("InMemCache Error: %v", err)
	}
	return &Cache{
		inMemCache:         cacheInstance,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		TTL:                TTL,
		ctx:                context.Background(),
	}
}

func (cr *Cache) WithContext(ctx context.Context) *Cache {
	return &Cache{
		baseRedisClient:    cr.baseRedisClient,
		slaveRedisClients:  cr.slaveRedisClients,
		inMemCache:         cr.inMemCache,
		amnesiaChance:      cr.amnesiaChance,
		compressionEnabled: cr.compressionEnabled,
		TTL:                cr.TTL,
		ctx:                ctx,
	}
}

func (cr *Cache) Get(key string) (interface{}, error) {
	if cr.amnesiaChance > rand.Intn(100) {
		return nil, errors.New("Had Amnesia")
	}
	var rawBytes []byte
	var err error
	if cr.inMemCache != nil {
		rawBytes, err = cr.inMemCache.Get(key)
	} else {
		var strValue string
		client := cr.pickClient().WithContext(cr.ctx)
		strValue, err = client.Get(key).Result()
		rawBytes = []byte(strValue)
	}
	if err != nil {
		return nil, err
	}
	var finalBytes []byte
	if cr.compressionEnabled {
		finalBytes = Decompress_zlib(rawBytes)
	} else {
		finalBytes = rawBytes
	}
	var finalObject interface{}
	msgpack.Unmarshal(finalBytes, &finalObject)
	return finalObject, nil
}

func (cr *Cache) Set(key string, value interface{}) error {
	if cr.amnesiaChance == 100 {
		return errors.New("Had Amnesia")
	}
	rawData, err := msgpack.Marshal(value)
	if err != nil {
		return err
	}
	var finalData []byte
	if cr.compressionEnabled {
		finalData = Compress_zlib(rawData)
	} else {
		finalData = rawData
	}
	if cr.inMemCache != nil {
		return cr.inMemCache.Set(key, finalData)
	}
	// else if Redis
	client := cr.baseRedisClient.WithContext(cr.ctx)
	err = client.SetNX(key, finalData, cr.TTL).Err()
	return err
}

func (cr *Cache) GetTTL(key string) time.Duration {
	if cr.inMemCache != nil {
		return time.Second * 0
	}
	client := cr.pickClient().WithContext(cr.ctx)
	res, err := client.TTL(key).Result()
	if err != nil {
		return time.Second * 0
	}
	return res

}

func (cr *Cache) pickClient() *redis.Client {
	if cr.slaveRedisClients == nil {
		return cr.baseRedisClient
	}
	cl := rand.Intn(len(cr.slaveRedisClients) + 1)
	if cl == 0 {
		return cr.baseRedisClient
	}
	return cr.slaveRedisClients[cl-1]
}
