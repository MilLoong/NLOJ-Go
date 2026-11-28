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
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type backends struct {
	db                                  *sql.DB
	orm                                 *gorm.DB
	cache                               *redis.Client
	cachePrefix                         string
	cacheVersion                        map[int]uint64
	cacheHits, cacheMisses, cacheErrors uint64
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
	for _, ddl := range schema {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			return nil, err
		}
	}
	if err := migrateLegacy(db); err != nil {
		return nil, err
	}
	s := newService()
	if err := s.loadTables(db); err != nil {
		return nil, err
	}
	orm, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, err
	}
	s.backends = &backends{db: db, orm: orm}
	ok = true
	return s, nil
}

func (b *backends) append(record journalRecord) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return b.orm.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return persist(tx, record) })
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
		b.cacheHits++
		return cached
	}
	if err == redis.Nil {
		b.cacheMisses++
	} else {
		b.cacheErrors++
	}
	p = p.PublicDetail()
	if data, err := json.Marshal(p); err == nil {
		b.cache.Set(ctx, key, data, 5*time.Minute)
	}
	return p
}
