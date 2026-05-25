package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchRealtimeAlwaysHitsNetwork(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("rt"))
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), NewCache(t.TempDir(), time.Hour), false, false)
	for i := 0; i < 2; i++ {
		b, err := f.FetchRealtime(context.Background(), srv.URL)
		if err != nil || string(b) != "rt" {
			t.Fatalf("got %q err %v", b, err)
		}
	}
	if hits != 2 {
		t.Errorf("realtime hits=%d want 2 (never cached)", hits)
	}
}

func TestFetchStaticUsesConditionalGET(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("If-None-Match") == "v1" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "v1")
		w.Write([]byte("ZIPDATA"))
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), NewCache(t.TempDir(), time.Hour), false, false)

	b1, err := f.FetchStatic(context.Background(), srv.URL)
	if err != nil || string(b1) != "ZIPDATA" {
		t.Fatalf("first fetch %q err %v", b1, err)
	}
	b2, err := f.FetchStatic(context.Background(), srv.URL)
	if err != nil || string(b2) != "ZIPDATA" {
		t.Fatalf("second fetch %q err %v", b2, err)
	}
	if hits != 2 {
		t.Errorf("static hits=%d want 2 (a 200 then a 304)", hits)
	}
}
