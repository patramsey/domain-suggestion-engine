package main

import (
	"strings"
	"testing"
)

func TestBatch(t *testing.T) {
	items := make([]item, 7)
	got := batch(items, 3)
	if len(got) != 3 || len(got[0]) != 3 || len(got[2]) != 1 {
		t.Fatalf("batch sizes = %d, %d…%d", len(got), len(got[0]), len(got[len(got)-1]))
	}
	if b := batch(nil, 3); b != nil {
		t.Errorf("batch(nil) = %v, want nil", b)
	}
}

func TestUserMessageListsEveryItem(t *testing.T) {
	msg := userMessage([]item{
		{ID: "r001", Query: "yoga studio", Domain: "mat.guru"},
		{ID: "r002", Query: "tea", Domain: "steep.shop"},
	})
	for _, want := range []string{"r001", "yoga studio", "mat.guru", "r002", "steep.shop"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func TestParseRatings(t *testing.T) {
	got, err := parseRatings(`[{"id":"r001","rating":"good"},{"id":"r002","rating":"BAD"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if got["r001"] != "good" || got["r002"] != "bad" {
		t.Errorf("got %v, want normalized good/bad", got)
	}
}

func TestParseRatingsRecoversFromProse(t *testing.T) {
	got, err := parseRatings("Here you go:\n```json\n[{\"id\":\"r001\",\"rating\":\"okay\"}]\n```")
	if err != nil || got["r001"] != "okay" {
		t.Errorf("got %v err=%v, want r001=okay", got, err)
	}
}

func TestParseRatingsRejectsUnknownValue(t *testing.T) {
	if _, err := parseRatings(`[{"id":"r001","rating":"excellent"}]`); err == nil {
		t.Error("want an error for a rating outside good/okay/bad")
	}
}
