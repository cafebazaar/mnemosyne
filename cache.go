package mnemosyne

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"git.cafebazaar.ir/bazaar/search/octopus/pkg/epimetheus"
	"github.com/allegro/bigcache"
	"github.com/go-redis/redis"
	"github.com/sirupsen/logrus"
)

type Cache struct {
	layerName          string
	baseRedisClient    *redis.Client
	slaveRedisClients  []*redis.Client
	inMemCache         *bigcache.BigCache
	syncmap            *sync.Map
	amnesiaChance      int
	compressionEnabled bool
	cacheTTL           time.Duration
	ctx                context.Context
	Timer              *epimetheus.Timer
}

func NewCacheRedis(layerName string, addr string, db int, TTL time.Duration, amnesiaChance int, compressionEnabled bool, timer *epimetheus.Timer) *Cache {
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
		layerName:          layerName,
		baseRedisClient:    redisClient,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		cacheTTL:           TTL,
		ctx:                context.Background(),
		Timer:              timer,
	}
}

func NewCacheClusterRedis(layerName string, masterAddr string, slaveAddrs []string, db int, TTL time.Duration, amnesiaChance int, compressionEnabled bool, timer *epimetheus.Timer) *Cache {
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
		layerName:          layerName,
		baseRedisClient:    redisClient,
		slaveRedisClients:  slaveClients,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		cacheTTL:           TTL,
		ctx:                context.Background(),
		Timer:              timer,
	}
}

func NewCacheInMem(layerName string, maxMem int, TTL time.Duration, amnesiaChance int, compressionEnabled bool) *Cache {
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
		layerName:          layerName,
		inMemCache:         cacheInstance,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		cacheTTL:           TTL,
		ctx:                context.Background(),
	}
}

func NewCacheTiny(layerName string, amnesiaChance int, compressionEnabled bool) *Cache {
	data := sync.Map{}
	return &Cache{
		layerName:          layerName,
		syncmap:            &data,
		amnesiaChance:      amnesiaChance,
		compressionEnabled: compressionEnabled,
		cacheTTL:           time.Hour * 9999,
		ctx:                context.Background(),
	}
}

func (cr *Cache) WithContext(ctx context.Context) *Cache {
	return &Cache{
		layerName:          cr.layerName,
		baseRedisClient:    cr.baseRedisClient,
		slaveRedisClients:  cr.slaveRedisClients,
		inMemCache:         cr.inMemCache,
		syncmap:            cr.syncmap,
		amnesiaChance:      cr.amnesiaChance,
		compressionEnabled: cr.compressionEnabled,
		cacheTTL:           cr.cacheTTL,
		ctx:                ctx,
		Timer:              cr.Timer,
	}
}

func (cr *Cache) Get(key string) (*cachableRet, error) {
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
		timer := cr.Timer.Start()
		strValue, err = client.Get(key).Result()
		if err == nil {
			timer.Done(cr.layerName, "get", "ok")
		} else if err == redis.Nil {
			timer.Done(cr.layerName, "get", "miss")
		} else {
			timer.Done(cr.layerName, "get", "error")
		}
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
	var finalObject cachableRet
	unmarshalErr := json.Unmarshal(finalBytes, &finalObject)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("failed to unmarshall cached value : %v", unmarshalErr)
	}
	return &finalObject, nil
}

func (cr *Cache) Set(key string, value interface{}) error {
	if cr.amnesiaChance == 100 {
		return errors.New("Had Amnesia")
	}
	rawData, err := json.Marshal(value)
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
	timer := cr.Timer.Start()
	err = client.SetNX(key, finalData, cr.cacheTTL).Err()
	if err != nil {
		timer.Done(cr.layerName, "set", "error")
	} else {
		timer.Done(cr.layerName, "set", "ok")
	}

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
	if len(cr.slaveRedisClients) == 0 {
		return cr.baseRedisClient
	}
	cl := rand.Intn(len(cr.slaveRedisClients) + 1)
	if cl == 0 {
		return cr.baseRedisClient
	}
	return cr.slaveRedisClients[cl-1]
}
