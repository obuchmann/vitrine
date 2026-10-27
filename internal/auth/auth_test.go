package auth

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLoadOrCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", FileName)
	tok, err := LoadOrCreate(path)
	if err != nil || !strings.HasPrefix(tok, prefix) {
		t.Fatalf("create: %q %v", tok, err)
	}
	again, err := LoadOrCreate(path)
	if err != nil || again != tok {
		t.Fatalf("load: %q %v, want %q", again, err, tok)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "Authorization: Bearer "+tok+"\nX-Vitrine: 1\n" {
		t.Fatalf("file is not a curl header file: %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode %v, want 0600", fi.Mode().Perm())
		}
	}
}

func TestConcurrentCreateAgrees(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	var wg sync.WaitGroup
	toks := make([]string, 8)
	for i := range toks {
		wg.Go(func() {
			tok, err := LoadOrCreate(path)
			if err != nil {
				t.Error(err)
			}
			toks[i] = tok
		})
	}
	wg.Wait()
	for _, tok := range toks {
		if tok != toks[0] {
			t.Fatalf("processes disagree on the token: %v", toks)
		}
	}
}

func TestBrokenFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	os.WriteFile(path, []byte("garbage\n"), 0o600)
	if _, err := LoadOrCreate(path); err == nil {
		t.Fatal("broken token file accepted")
	}
}

func TestBearerAndEqual(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer abc")
	if Bearer(r) != "abc" || !Equal("abc", "abc") || Equal("abc", "abd") || Equal("", "") {
		t.Fatal("bearer/equal")
	}
}
