package mnemosyne

import (
	"context"
	"hash/fnv"
	"math/rand"
	"time"

	"github.com/go-redis/redis"
	"github.com/sirupsen/logrus"
)

type RedisClusterAddress struct {
	MasterAddr string   `mapstructure:"address"`
	SlaveAddrs []string `mapstructure:"slaves"`
}

type RedisOpts struct {
	db          int
	idleTimeout time.Duration
	shards      []*RedisClusterAddress
}

type clusterClient struct {
	master *redis.Client
	slaves []*redis.Client
}

type redisCache struct {
	layerName          string
	baseClients        []*clusterClient
	amnesiaChance      int
	compressionEnabled bool
	cacheTTL           time.Duration
	watcher            ITimer
}

func makeClient(addr string, db int, idleTimeout time.Duration) *redis.Client {
	redisOptions := &redis.Options{
		Addr: addr,
		DB:   db,
	}
	if idleTimeout >= time.Second {
		redisOptions.IdleTimeout = idleTimeout
	}
	newClient := redis.NewClient(redisOptions)

	if err := newClient.Ping().Err(); err != nil {
		logrus.WithError(err).WithField("address", addr).Error("error pinging Redis")
	}
	return newClient
}

func NewShardedClusterRedisCache(opts *CacheOpts, watcher ITimer) *redisCache {
	rc := &redisCache{
		layerName:          opts.layerName,
		amnesiaChance:      opts.amnesiaChance,
		compressionEnabled: opts.compressionEnabled,
		cacheTTL:           opts.cacheTTL,
		watcher:            watcher,
	}
	rc.baseClients = make([]*clusterClient, len(opts.redisOpts.shards))
	for i, shard := range opts.redisOpts.shards {
		rc.baseClients[i].master = makeClient(shard.MasterAddr,
			opts.redisOpts.db,
			opts.redisOpts.idleTimeout)

		rc.baseClients[i].slaves = make([]*redis.Client, len(shard.SlaveAddrs))
		for j, slv := range shard.SlaveAddrs {
			rc.baseClients[i].slaves[j] = makeClient(slv,
				opts.redisOpts.db,
				opts.redisOpts.idleTimeout)
		}
	}
	return rc
}

func (rc *redisCache) Get(ctx context.Context, key string) (*cachableRet, error) {
	if rc.amnesiaChance > rand.Intn(100) {
		return nil, NewAmnesiaError(rc.amnesiaChance)
	}
	client := rc.pickClient(key, false).WithContext(ctx)
	startMarker := rc.watcher.Start()
	strValue, err := client.Get(key).Result()
	if err == nil {
		rc.watcher.Done(startMarker, rc.layerName, "get", "ok")
	} else if err == redis.Nil {
		rc.watcher.Done(startMarker, rc.layerName, "get", "miss")
	} else {
		rc.watcher.Done(startMarker, rc.layerName, "get", "error")
	}
	rawBytes := []byte(strValue)
	return finalizeCacheResponse(rawBytes, rc.compressionEnabled)
}

func (rc *redisCache) Set(ctx context.Context, key string, value interface{}) error {
	if rc.amnesiaChance == 100 {
		return NewAmnesiaError(rc.amnesiaChance)
	}
	finalData, err := prepareCachePayload(value, rc.compressionEnabled)
	if err != nil {
		return err
	}
	client := rc.pickClient(key, true).WithContext(ctx)
	startMarker := rc.watcher.Start()
	setError := client.SetNX(key, finalData, rc.cacheTTL).Err()
	if setError != nil {
		rc.watcher.Done(startMarker, rc.layerName, "set", "error")
	} else {
		rc.watcher.Done(startMarker, rc.layerName, "set", "ok")
	}
	return setError
}
func (rc *redisCache) Delete(ctx context.Context, key string) error {
	if rc.amnesiaChance == 100 {
		return NewAmnesiaError(rc.amnesiaChance)
	}
	client := rc.pickClient(key, true).WithContext(ctx)
	return client.Del(key).Err()
}

func (rc *redisCache) Clear() error {
	for _, cl := range rc.baseClients {
		client := cl.master
		err := client.FlushDB().Err()
		if err != nil {
			return err
		}
	}
	return nil
}

func (rc *redisCache) TTL(ctx context.Context, key string) time.Duration {
	client := rc.pickClient(key, false).WithContext(ctx)
	res, err := client.TTL(key).Result()
	if err != nil {
		return time.Second * 0
	}
	return res
}

func (rc *redisCache) pickClient(key string, modification bool) *redis.Client {
	shard := rc.shardKey(key)
	if modification {
		return rc.baseClients[shard].master
	}
	cl := rand.Intn(len(rc.baseClients[shard].slaves))
	return rc.baseClients[shard].slaves[cl]
}

func (rc *redisCache) shardKey(key string) int {
	shards := len(rc.baseClients)
	if shards == 1 {
		return 0
	}
	hasher := fnv.New32a()
	hasher.Write([]byte(key))
	keyHash := int(hasher.Sum32())
	return keyHash % shards
}

func (rc *redisCache) Name() string {
	return rc.layerName
}
