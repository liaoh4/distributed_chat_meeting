package chat

import (
	"encoding/json"
	"errors"
	"sync"

	"go.etcd.io/bbolt"
)

var bucketMessages = []byte("messages")

type Store struct {
	db *bbolt.DB
	mu sync.RWMutex
}

func NewStore(path string) (*Store, error) {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	db.Update(func(tx *bbolt.Tx) error {
		_, _ = tx.CreateBucketIfNotExists(bucketMessages)
		return nil
	})
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Put(commitIndex uint64, value any) error {
	bs, err := json.Marshal(value)
	if err != nil {
		return err
	}
	key := make([]byte, 8)
	for i := uint(0); i < 8; i++ {
		key[7-i] = byte(commitIndex >> (i * 8))
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketMessages)
		return b.Put(key, bs)
	})
}

func (s *Store) Range(from uint64, fn func(idx uint64, raw []byte) error) error {
	return s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(bucketMessages)
		if b == nil {
			return errors.New("bucket not found")
		}
		c := b.Cursor()
		key := make([]byte, 8)
		for i := uint(0); i < 8; i++ {
			key[7-i] = byte(from >> (i * 8))
		}
		for k, v := c.Seek(key); k != nil; k, v = c.Next() {
			var idx uint64
			for i := 0; i < 8; i++ {
				idx = (idx << 8) | uint64(k[i])
			}
			if err := fn(idx, v); err != nil {
				return err
			}
		}
		return nil
	})
}
