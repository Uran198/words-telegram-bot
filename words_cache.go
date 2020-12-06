package main

import (
	"math/rand"
	"sync"

	"github.com/golang/groupcache/lru"
)

// WordsCache will map arbitrary ids to the text. Main use case is for the
// buttons that need to refer to the front of the card, and front of the card
// is > 64 bytes making it not possible to store in the callback_data.
type WordsCache struct {
	maxEntries int

	mu sync.Mutex
	c  map[int64]*lru.Cache
}

func NewWordsCache(maxEntries int) *WordsCache {
	return &WordsCache{
		maxEntries: maxEntries,
		c:          make(map[int64]*lru.Cache),
	}
}

func (wc *WordsCache) cache(chatID int64) *lru.Cache {
	c := wc.c[chatID]
	if c == nil {
		c = lru.New(wc.maxEntries)
		wc.c[chatID] = c
	}
	return c
}

func (wc *WordsCache) Add(chatID int64, front string) (id int64) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	id = rand.Int63()
	wc.cache(chatID).Add(id, front)
	return id
}

func (wc *WordsCache) Get(chatID, id int64) (front string, ok bool) {
	wc.mu.Lock()
	defer wc.mu.Unlock()
	var v interface{}
	if v, ok = wc.cache(chatID).Get(id); !ok {
		return
	}
	return v.(string), ok
}
