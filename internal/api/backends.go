package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
)

type backends struct {
	db           *sql.DB
	cache        *redis.Client
	cachePrefix  string
	cacheVersion map[int]uint64
}

func openMySQL(dsn string) (*sql.DB, error) { return sql.Open("mysql", dsn) }

func mysqlService(dsn string) (*service, error) {
	db, err := openMySQL(dsn)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			db.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS nloj_go_events (seq BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, payload JSON NOT NULL)",
		"CREATE TABLE IF NOT EXISTS nloj_go_judge_results (job_key CHAR(64) NOT NULL PRIMARY KEY, payload JSON NOT NULL)",
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return nil, err
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT payload FROM nloj_go_events ORDER BY seq")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []journalRecord
	for rows.Next() {
		var payload []byte
		var record journalRecord
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &record); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s := newService()
	if err := s.replay(records); err != nil {
		return nil, err
	}
	s.backends = &backends{db: db}
	ok = true
	return s, nil
}

func (b *backends) append(record journalRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = b.db.ExecContext(ctx, "INSERT INTO nloj_go_events (payload) VALUES (?)", payload)
	return err
}

func (s *service) enableCache(rawURL string) error {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		return err
	}
	opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = 150*time.Millisecond, 150*time.Millisecond, 150*time.Millisecond
	opts.MaxRetries = -1
	if s.backends == nil {
		s.backends = &backends{}
	}
	var nonce [16]byte
	rand.Read(nonce[:])
	s.backends.cache = redis.NewClient(opts)
	s.backends.cachePrefix = "nloj-go:public-problem:" + hex.EncodeToString(nonce[:]) + ":"
	s.backends.cacheVersion = make(map[int]uint64)
	return nil
}

func (s *service) publicProblem(id int) problem {
	p := s.problems[id-1]
	b := s.backends
	if b == nil || b.cache == nil {
		return p.PublicDetail()
	}
	key := b.cachePrefix + strconv.Itoa(id) + ":" + fmt.Sprint(b.cacheVersion[id])
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var cached problem
	raw, err := b.cache.Get(ctx, key).Bytes()
	if err == nil && json.Unmarshal(raw, &cached) == nil {
		return cached
	}
	p = p.PublicDetail()
	if data, err := json.Marshal(p); err == nil {
		b.cache.Set(ctx, key, data, 5*time.Minute)
	}
	return p
}
