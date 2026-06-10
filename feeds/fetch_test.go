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
		b, err := f.FetchRealtime(context.Background(), srv.URL, nil)
		if err != nil || string(b) != "rt" {
			t.Fatalf("got %q err %v", b, err)
		}
	}
	if hits != 2 {
		t.Errorf("realtime hits=%d want 2 (never cached)", hits)
	}
}

func TestFetchRealtimeSendsHeaders(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if _, err := w.Write([]byte("rt")); err != nil {
			t.Errorf("write failed: %v", err)
		}
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), NewCache(t.TempDir(), time.Hour), false, false)
	hdr := http.Header{}
	hdr.Set("Authorization", "secret-key")
	if _, err := f.FetchRealtime(context.Background(), srv.URL, hdr); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "secret-key" {
		t.Errorf("Authorization header = %q, want secret-key", gotAuth)
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

func TestFetchStaticTTLServesWithoutNetwork(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("DATA")) // no ETag, no Last-Modified
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), NewCache(t.TempDir(), time.Hour), false, false)

	if _, err := f.FetchStatic(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := f.FetchStatic(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("hits=%d want 1 (second call served from cache via TTL)", hits)
	}
}
