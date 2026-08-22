package model

import "testing"

func TestNewRouter_DefaultFromFirst(t *testing.T) {
	r := NewRouter([]ModelRoute{{MatchIntent: "normal", Model: "m1", APIKey: "k1"}})
	if r.def.Model != "m1" {
		t.Fatalf("default should be first route, got %q", r.def.Model)
	}
}

func TestSelect_SpecificUsable(t *testing.T) {
	r := NewRouter([]ModelRoute{
		{MatchIntent: "default", Model: "base", APIKey: "k0"},
		{MatchIntent: "deep", Model: "big", APIKey: "k1"},
	})
	got := r.Select("deep")
	if got.Model != "big" {
		t.Fatalf("expected deep route big, got %q", got.Model)
	}
}

func TestSelect_FallbackToDefaultWhenSpecificMissing(t *testing.T) {
	r := NewRouter([]ModelRoute{
		{MatchIntent: "default", Model: "base", APIKey: "k0"},
	})
	got := r.Select("team")
	if got.Model != "base" {
		t.Fatalf("expected fallback base, got %q", got.Model)
	}
}

func TestSelect_FallbackWhenSpecificUnusable(t *testing.T) {
	r := NewRouter([]ModelRoute{
		{MatchIntent: "default", Model: "base", APIKey: "k0"},
		{MatchIntent: "deep", Model: "big", APIKey: ""}, // no key → unusable
	})
	got := r.Select("deep")
	if got.Model != "base" {
		t.Fatalf("expected usable default base, got %q", got.Model)
	}
}

func TestSelect_ReturnsUnusableWhenNothingUsable(t *testing.T) {
	r := NewRouter([]ModelRoute{
		{MatchIntent: "default", Model: "", APIKey: ""},
	})
	got := r.Select("normal")
	if got.Usable() {
		t.Fatal("expected unusable route")
	}
	if got.Model != "" {
		t.Fatalf("expected empty model, got %q", got.Model)
	}
}

func TestRouter_Validate(t *testing.T) {
	bad := NewRouter([]ModelRoute{{MatchIntent: "default", Model: "", APIKey: ""}})
	if bad.Validate() == "" {
		t.Fatal("expected validation error for no usable route")
	}
	good := NewRouter([]ModelRoute{{MatchIntent: "default", Model: "m", APIKey: "k"}})
	if good.Validate() != "" {
		t.Fatalf("expected no error, got %q", good.Validate())
	}
}
