package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/redis/go-redis/v9"
)

// KeyframeStore manages 30-second pre-aggregated I-frames for sub-120ms time-travel scrubbing.
type KeyframeStore interface {
	SaveKeyframe(ctx context.Context, bucketEpoch30s int64, state *types.PointInTimeState) error
	GetKeyframe(ctx context.Context, bucketEpoch30s int64) (*types.PointInTimeState, error)
	GetNearestPriorKeyframe(ctx context.Context, timestampUnixNano int64) (int64, *types.PointInTimeState, error)
	Close() error
}

// InMemoryKeyframeStore stores 30-second keyframes in memory for embedded mode.
type InMemoryKeyframeStore struct {
	mu        sync.RWMutex
	keyframes map[int64]*types.PointInTimeState
	buckets   []int64
}

// NewInMemoryKeyframeStore creates an in-memory keyframe cache.
func NewInMemoryKeyframeStore() *InMemoryKeyframeStore {
	return &InMemoryKeyframeStore{
		keyframes: make(map[int64]*types.PointInTimeState),
		buckets:   make([]int64, 0),
	}
}

// SaveKeyframe stores a 30s keyframe snapshot.
func (s *InMemoryKeyframeStore) SaveKeyframe(ctx context.Context, bucketEpoch30s int64, state *types.PointInTimeState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.keyframes[bucketEpoch30s]; !exists {
		s.buckets = append(s.buckets, bucketEpoch30s)
		sort.Slice(s.buckets, func(i, j int) bool {
			return s.buckets[i] < s.buckets[j]
		})
	}
	s.keyframes[bucketEpoch30s] = state
	return nil
}

// GetKeyframe returns the exact keyframe if found.
func (s *InMemoryKeyframeStore) GetKeyframe(ctx context.Context, bucketEpoch30s int64) (*types.PointInTimeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, exists := s.keyframes[bucketEpoch30s]
	if !exists {
		return nil, fmt.Errorf("keyframe not found for bucket %d", bucketEpoch30s)
	}
	return state, nil
}

// GetNearestPriorKeyframe finds the closest keyframe Tk <= T where T - Tk < 30s.
func (s *InMemoryKeyframeStore) GetNearestPriorKeyframe(ctx context.Context, timestampUnixNano int64) (int64, *types.PointInTimeState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	targetSec := timestampUnixNano / 1e9
	targetBucket := (targetSec / 30) * 30

	if state, ok := s.keyframes[targetBucket]; ok {
		return targetBucket, state, nil
	}

	// Binary search for largest bucket <= targetBucket
	idx := sort.Search(len(s.buckets), func(i int) bool {
		return s.buckets[i] > targetBucket
	})

	if idx > 0 {
		nearestBucket := s.buckets[idx-1]
		// Check that it's within 60 seconds
		if targetBucket-nearestBucket <= 60 {
			return nearestBucket, s.keyframes[nearestBucket], nil
		}
	}

	return 0, nil, nil // No close prior keyframe, will fall back to cold aggregation
}

// Close cleans up in-memory data.
func (s *InMemoryKeyframeStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyframes = nil
	s.buckets = nil
	return nil
}

// RedisKeyframeStore stores 30-second keyframes in Redis using sorted sets for O(log N) retrieval.
type RedisKeyframeStore struct {
	client *redis.Client
}

// NewRedisKeyframeStore creates a Redis keyframe adapter.
func NewRedisKeyframeStore(redisURL string) (*RedisKeyframeStore, error) {
	if redisURL == "" {
		redisURL = "localhost:6379"
	}
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		opts = &redis.Options{Addr: redisURL}
	}
	rdb := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping failed: %w", err)
	}

	return &RedisKeyframeStore{client: rdb}, nil
}

// SaveKeyframe writes JSON state to Redis and indexes the bucket in a sorted set.
func (r *RedisKeyframeStore) SaveKeyframe(ctx context.Context, bucketEpoch30s int64, state *types.PointInTimeState) error {
	key := fmt.Sprintf("chronotrace:keyframe:%d", bucketEpoch30s)
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	pipe := r.client.Pipeline()
	pipe.Set(ctx, key, data, 24*time.Hour)
	pipe.ZAdd(ctx, "chronotrace:keyframes:zset", redis.Z{
		Score:  float64(bucketEpoch30s),
		Member: bucketEpoch30s,
	})
	_, err = pipe.Exec(ctx)
	return err
}

// GetKeyframe gets a specific 30s bucket keyframe.
func (r *RedisKeyframeStore) GetKeyframe(ctx context.Context, bucketEpoch30s int64) (*types.PointInTimeState, error) {
	key := fmt.Sprintf("chronotrace:keyframe:%d", bucketEpoch30s)
	data, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}

	var state types.PointInTimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// GetNearestPriorKeyframe uses ZREVRANGEBYSCORE to retrieve the closest keyframe Tk <= T in <1ms.
func (r *RedisKeyframeStore) GetNearestPriorKeyframe(ctx context.Context, timestampUnixNano int64) (int64, *types.PointInTimeState, error) {
	targetSec := timestampUnixNano / 1e9
	targetBucket := float64((targetSec / 30) * 30)

	res, err := r.client.ZRevRangeByScore(ctx, "chronotrace:keyframes:zset", &redis.ZRangeBy{
		Max:    fmt.Sprintf("%f", targetBucket),
		Min:    fmt.Sprintf("%f", targetBucket-60),
		Offset: 0,
		Count:  1,
	}).Result()
	if err != nil || len(res) == 0 {
		return 0, nil, nil
	}

	var bucket int64
	fmt.Sscanf(res[0], "%d", &bucket)
	state, err := r.GetKeyframe(ctx, bucket)
	if err != nil {
		return 0, nil, nil
	}
	return bucket, state, nil
}

// Close closes the Redis connection.
func (r *RedisKeyframeStore) Close() error {
	return r.client.Close()
}
