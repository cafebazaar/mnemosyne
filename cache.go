package mnemosyne

import (
	"context"
	"errors"
	"math/rand"
	"sync"
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
	syncmap            *sync.Map
	amnesiaChance      int
	compressionEnabled bool
	cacheTTL           time.Duration
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
		cacheTTL:           TTL,
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
		cacheTTL:           TTL,
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
		cacheTTL:           TTL,
		ctx:                context.Background(),
	}
}

func NewCacheTiny(amnesiaChance int, compressionEnabled bool) *Cache {
	data := sync.Map{}
	return &Cache{
		syncmap:            &data,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		cacheTTL:           time.Hour * 9999,
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
		cacheTTL:           cr.cacheTTL,
		ctx:                ctx,
	}
}

func (cr *Cache) Get(key string) (interface{}, error) {
	if cr.amnesiaChance > rand.Intn(100) {
		return nil, errors.New("Had Amnesia")
	}
	var rawBytes []byte
	var err error
	if cr.syncmap != nil {
		val, ok := cr.syncmap.Load(key)
		if !ok {
			err = errors.New("Failed to load from syncmap")
		} else {
			rawBytes, ok = val.([]byte)
			if !ok {
				err = errors.New("Failed to load from syncmap")
			}
		}
	} else if cr.inMemCache != nil {
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
		finalBytes = DecompressZlib(rawBytes)
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
		finalData = CompressZlib(rawData)
	} else {
		finalData = rawData
	}
	if cr.syncmap != nil {
		cr.syncmap.Store(key, finalData)
		return nil
	} else if cr.inMemCache != nil {
		return cr.inMemCache.Set(key, finalData)
	}
	client := cr.baseRedisClient.WithContext(cr.ctx)
	err = client.SetNX(key, finalData, cr.cacheTTL).Err()
	return err
}

func (cr *Cache) Delete(ctx context.Context, key string) error {
	if cr.amnesiaChance == 100 {
		return errors.New("Had Amnesia")
	}
	if cr.syncmap != nil {
		cr.syncmap.Delete(key)
	} else if cr.inMemCache != nil {
		return cr.inMemCache.Delete(key)
	}
	client := cr.baseRedisClient.WithContext(ctx)
	err := client.Del(key).Err()
	return err
}

func (cr *Cache) Clear(ctx context.Context) error {
	if cr.amnesiaChance == 100 {
		return errors.New("Had Amnesia")
	}
	if cr.syncmap != nil {
		cr.syncmap = &sync.Map{}
	} else if cr.inMemCache != nil {
		return cr.inMemCache.Reset()
	}
	client := cr.baseRedisClient.WithContext(ctx)
	err := client.FlushDB().Err()
	return err
}

func (cr *Cache) TTL(key string) time.Duration {
	if cr.inMemCache != nil || cr.syncmap != nil {
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
