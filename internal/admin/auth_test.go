package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestWriteAndLoadHashFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "admin.hash")
	if err := WriteHashFile(p, "hunter2"); err != nil {
		t.Fatal(err)
	}
	got, err := LoadHashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	ba, err := NewBasicAuth(got)
	if err != nil {
		t.Fatalf("NewBasicAuth: %v", err)
	}
	if ba == nil {
		t.Fatal("nil BasicAuth")
	}
}

func TestNewBasicAuthRejectsJunk(t *testing.T) {
	if _, err := NewBasicAuth(""); err == nil {
		t.Error("empty hash should error")
	}
	if _, err := NewBasicAuth("not-a-bcrypt-hash"); err == nil {
		t.Error("invalid hash should error")
	}
}

func TestWrapAllowsCorrectPassword(t *testing.T) {
	p := filepath.Join(t.TempDir(), "admin.hash")
	if err := WriteHashFile(p, "hunter2"); err != nil {
		t.Fatal(err)
	}
	hash, _ := LoadHashFile(p)
	ba, _ := NewBasicAuth(hash)

	var reached bool
	h := ba.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.SetBasicAuth("admin", "hunter2")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if !reached || w.Code != 200 {
		t.Fatalf("expected 200+reached, got code=%d reached=%v", w.Code, reached)
	}
}

func TestWrapRejectsNoAuth(t *testing.T) {
	hash, _ := func() (string, error) {
		p := filepath.Join(t.TempDir(), "h")
		if err := WriteHashFile(p, "x"); err != nil {
			return "", err
		}
		return LoadHashFile(p)
	}()
	ba, _ := NewBasicAuth(hash)
	h := ba.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("should not reach") }))

	req := httptest.NewRequest("GET", "/admin", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("code=%d", w.Code)
	}
	if w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("WWW-Authenticate header missing")
	}
}

func TestWrapRejectsWrongPassword(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h")
	_ = WriteHashFile(p, "correct")
	hash, _ := LoadHashFile(p)
	ba, _ := NewBasicAuth(hash)
	h := ba.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("should not reach") }))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.SetBasicAuth("admin", "wrong")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("code=%d", w.Code)
	}
}

func TestWrapRejectsWrongUsername(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h")
	_ = WriteHashFile(p, "pw")
	hash, _ := LoadHashFile(p)
	ba, _ := NewBasicAuth(hash)
	h := ba.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("should not reach") }))

	req := httptest.NewRequest("GET", "/admin", nil)
	req.SetBasicAuth("root", "pw")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("code=%d", w.Code)
	}
}
