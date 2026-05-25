package feeds

import (
	"os"
	"testing"
	"time"
)

func TestCacheStoreAndLoad(t *testing.T) {
	c := NewCache(t.TempDir(), time.Hour)
	k := key("https://x/gtfs.zip")
	if err := c.store(k, []byte("BODY"), cacheMeta{URL: "u", ETag: "e", FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	body, meta, ok := c.load(k)
	if !ok || string(body) != "BODY" || meta.ETag != "e" {
		t.Fatalf("load got ok=%v body=%q meta=%+v", ok, body, meta)
	}
}

func TestCacheBodyWithoutMetaIsMiss(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, time.Hour)
	k := key("u")
	if err := os.WriteFile(c.bodyPath(k), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.load(k); ok {
		t.Error("orphan body should be a miss")
	}
}

func TestMetaFreshOnlyWithoutValidators(t *testing.T) {
	withValidator := &cacheMeta{ETag: "e", FetchedAt: time.Now()}
	if withValidator.fresh(time.Hour) {
		t.Error("entry with ETag must not be TTL-fresh")
	}
	noValidator := &cacheMeta{FetchedAt: time.Now()}
	if !noValidator.fresh(time.Hour) {
		t.Error("recent no-validator entry should be fresh")
	}
	stale := &cacheMeta{FetchedAt: time.Now().Add(-2 * time.Hour)}
	if stale.fresh(time.Hour) {
		t.Error("old no-validator entry should be stale")
	}
}
