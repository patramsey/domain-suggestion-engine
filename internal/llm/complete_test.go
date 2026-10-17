package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The key must never be in the URL: a transport error prints the URL, and
// those errors are logged.
func TestAPIKeyTravelsInHeaderNotURL(t *testing.T) {
	var gotKey, gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotURL = r.Header.Get("x-goog-api-key"), r.URL.String()
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"[]"}]}}]}`))
	}))
	defer srv.Close()

	c := NewClient("SECRET-KEY", "m")
	c.BaseURL = srv.URL + "/"
	if _, _, err := c.Complete(context.Background(), "sys", "user"); err != nil {
		t.Fatal(err)
	}
	if gotKey != "SECRET-KEY" {
		t.Errorf("x-goog-api-key = %q, want the key", gotKey)
	}
	if strings.Contains(gotURL, "SECRET-KEY") {
		t.Errorf("key leaked into the URL: %s", gotURL)
	}
}

func TestTransportErrorDoesNotContainKey(t *testing.T) {
	c := NewClient("SECRET-KEY", "m")
	c.BaseURL = "http://127.0.0.1:1/" // nothing listens here
	_, _, err := c.Complete(context.Background(), "sys", "user")
	if err == nil {
		t.Fatal("want a transport error")
	}
	if strings.Contains(err.Error(), "SECRET-KEY") {
		t.Errorf("error text contains the key: %v", err)
	}
}
