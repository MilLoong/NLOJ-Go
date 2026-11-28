package api

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestRedisPublicProblem(t *testing.T) {
	rawURL := os.Getenv("NLOJ_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("set NLOJ_TEST_REDIS_URL for Redis integration")
	}
	s := newService()
	if err := s.enableCache(rawURL); err != nil {
		t.Fatal(err)
	}
	defer s.backends.cache.Close()
	s.problems = []problem{{ID: 1, Title: "original", Extra: "checker-source", Cases: []testCase{
		{Input: "public", Sample: true}, {Input: "private"},
	}}}
	first := s.publicProblem(1)
	if len(first.Cases) != 1 || first.Cases[0].Input != "public" {
		t.Fatalf("wrong public cases: %+v", first.Cases)
	}
	key := s.backends.cachePrefix + "1:0"
	prior, err := s.backends.cache.Get(context.Background(), key).Result()
	if err != nil || strings.Contains(prior, "private") || strings.Contains(prior, "checker-source") {
		t.Fatalf("invalid public cache entry: %v", err)
	}
	s.problems[0].Title = "updated"
	if hit := s.publicProblem(1); hit.Title != "original" {
		t.Fatalf("did not hit cache: %q", hit.Title)
	}
	s.backends.cacheVersion[1]++
	if fresh := s.publicProblem(1); fresh.Title != "updated" {
		t.Fatalf("read stale data after invalidation: %q", fresh.Title)
	}
	b := s.backends
	if b.cacheHits != 1 || b.cacheMisses != 2 || b.cacheErrors != 0 {
		t.Fatalf("cache counters: hits=%d misses=%d errors=%d", b.cacheHits, b.cacheMisses, b.cacheErrors)
	}
}
