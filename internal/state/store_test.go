package state

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUpdateIsAtomicUnderLock(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "state.json"))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := store.Update(func(file *File) error {
				file.Put(Preview{ID: strconv.Itoa(i), Hostname: "h" + strconv.Itoa(i)})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	file, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Previews) != 20 {
		t.Fatalf("len %d", len(file.Previews))
	}
}

func TestFindAndRemove(t *testing.T) {
	file := File{}
	expires := time.Now().Add(time.Hour).UTC()
	file.Put(Preview{
		ID:           "pv_k7p2",
		Hostname:     "app--fix--k7p2.preview.example.com",
		CaddyRouteID: "preview-pv-k7p2",
		TargetHost:   "127.0.0.1",
		TargetPort:   3000,
		ExpiresAt:    &expires,
	})
	got, ok := file.Find("https://app--fix--k7p2.preview.example.com/")
	if !ok || got.ID != "pv_k7p2" {
		t.Fatalf("%+v %v", got, ok)
	}
	if got.PublicURL() != "https://app--fix--k7p2.preview.example.com" || got.Dial() != "127.0.0.1:3000" {
		t.Fatalf("url %s dial %s", got.PublicURL(), got.Dial())
	}
	if !file.Remove("pv_k7p2") || len(file.Previews) != 0 {
		t.Fatal("remove failed")
	}
}

func TestResolvePrefix(t *testing.T) {
	file := File{}
	file.Put(Preview{ID: "pv_k7p2", Hostname: "app--fix--k7p2.preview.example.com", CaddyRouteID: "preview-pv-k7p2"})
	file.Put(Preview{ID: "pv_k7aa", Hostname: "app--fix--k7aa.preview.example.com", CaddyRouteID: "preview-pv-k7aa"})
	file.Put(Preview{ID: "pv_8svq", Hostname: "root--hello--8svq.preview.example.com", CaddyRouteID: "preview-pv-8svq"})

	got, err := file.Resolve("k7p")
	if err != nil || got.ID != "pv_k7p2" {
		t.Fatalf("%s %v", got.ID, err)
	}
	got, err = file.Resolve("8svq")
	if err != nil || got.ID != "pv_8svq" {
		t.Fatalf("%s %v", got.ID, err)
	}
	if _, err := file.Resolve("k7"); err == nil || !strings.Contains(err.Error(), "pv_k7p2") || !strings.Contains(err.Error(), "pv_k7aa") {
		t.Fatalf("%v", err)
	}
	if _, err := file.Resolve("missing"); err == nil {
		t.Fatal("expected a miss")
	}
}

func TestIPv6Dial(t *testing.T) {
	p := Preview{TargetHost: "::1", TargetPort: 8080}
	if p.Dial() != "[::1]:8080" {
		t.Fatal(p.Dial())
	}
}
