package kavel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGenerateRejectsEmptyPrompt(t *testing.T) {
	if _, err := Generate(context.Background(), "   ", Options{}); err == nil {
		t.Fatal("want an error for a blank prompt, got nil")
	}
}

func TestEditValidatesArguments(t *testing.T) {
	if _, err := Edit(context.Background(), "not-a-url", "make it blue", Options{}); err == nil {
		t.Fatal("want an error for a non-http source url, got nil")
	}
	if _, err := Edit(context.Background(), "https://example.com/a.png", "", Options{}); err == nil {
		t.Fatal("want an error for a blank instruction, got nil")
	}
}

func TestAnonIDIsFreshPerCall(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := anonID()
		if !strings.HasPrefix(id, "go-") {
			t.Fatalf("client id %q lost its prefix", id)
		}
		if seen[id] {
			t.Fatalf("client id %q was minted twice; the grant would wall mid-loop", id)
		}
		seen[id] = true
	}
}

// The quota wall answers 200 with code 0, so it can only be recognised by the
// `wall` field. Losing this branch turns it into "no task id".
func TestWallMapsToErrQuota(t *testing.T) {
	for _, tc := range []struct {
		reason string
		want   error
	}{
		{"anon_credits", ErrQuota},
		{"anon_ip_daily", ErrQuota},
		{"anon_unmetered_video", ErrSignIn},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"code":0,"message":"ok","data":{"wall":true,"reason":"` + tc.reason + `"}}`))
		}))
		_, err := postFor(t, srv)
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Fatalf("reason %q: got %v, want %v", tc.reason, err, tc.want)
		}
	}
}

// A refusal is HTTP 200 with code -1; only the message says whether an account
// would have helped.
func TestSignInRefusalMapsToErrSignIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"code":-1,"message":"Please sign in — sign up for free credits to use this."}`))
	}))
	defer srv.Close()
	if _, err := postFor(t, srv); !errors.Is(err, ErrSignIn) {
		t.Fatalf("got %v, want ErrSignIn", err)
	}
}

func TestHeadersCarryTheClientID(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("x-anon-id")
		w.Write([]byte(`{"code":0,"message":"ok","data":{"wall":true,"reason":"anon_credits"}}`))
	}))
	defer srv.Close()
	postFor(t, srv)
	if id := <-got; !strings.HasPrefix(id, "go-") {
		t.Fatalf("x-anon-id was %q; the service cannot meter the run without it", id)
	}
}

// A dropped poll mid-queue must not be read as a failed generation: the job is
// about to be paid for and the deadline is what should end the wait.
func TestTransientPollErrorDoesNotAbandonTheJob(t *testing.T) {
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Write([]byte(`{"code":0,"message":"ok","data":{"id":"t1","status":"pending"}}`))
			return
		}
		polls++
		if polls == 1 {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			conn.Close() // the exact shape observed against the live service
			return
		}
		w.Write([]byte(`{"code":0,"message":"ok","data":{"status":"success","images":["https://cdn.kavel.ai/x.webp"],"watermarked":[true]}}`))
	}))
	defer srv.Close()

	defer swapBase(srv.URL)()

	img, err := Generate(context.Background(), "a mug", Options{PollEvery: 10 * time.Millisecond, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("a single dropped poll ended the call: %v", err)
	}
	if img.URL == "" || !img.Watermarked {
		t.Fatalf("got %+v, want the url and the watermark flag", img)
	}
}

func TestDeadlineIsRespected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Write([]byte(`{"code":0,"message":"ok","data":{"id":"t1","status":"pending"}}`))
			return
		}
		w.Write([]byte(`{"code":0,"message":"ok","data":{"status":"processing","images":[],"queued":true}}`))
	}))
	defer srv.Close()

	defer swapBase(srv.URL)()

	start := time.Now()
	if _, err := Generate(context.Background(), "a mug", Options{PollEvery: 5 * time.Millisecond, Timeout: 120 * time.Millisecond}); err == nil {
		t.Fatal("want a deadline error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("the call ran %v past its own deadline", elapsed)
	}
}

// swapBase points the package at a stub and returns the restore func.
func swapBase(url string) func() {
	old := BaseURL
	BaseURL = url
	return func() { BaseURL = old }
}

func postFor(t *testing.T, srv *httptest.Server) (Image, error) {
	t.Helper()
	defer swapBase(srv.URL)()
	return Generate(context.Background(), "a mug", Options{PollEvery: time.Millisecond, Timeout: time.Second})
}
