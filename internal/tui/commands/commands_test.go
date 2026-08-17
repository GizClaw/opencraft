package commands

import "testing"

func TestLookupAndList(t *testing.T) {
	if _, ok := Lookup("resume"); !ok {
		t.Error("resume should be registered")
	}
	c, ok := Lookup("permissions")
	if !ok || c.Desc == "" {
		t.Errorf("permissions should have a description: %+v", c)
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("nope should not be registered")
	}
	if _, ok := Lookup("skills"); !ok {
		t.Error("skills should be registered")
	}
	if got := len(List()); got != 3 {
		t.Errorf("List() = %d commands, want 3", got)
	}
}

func TestIndexSearch(t *testing.T) {
	ix := NewIndex()
	res := ix.Search("sandbox", 10)
	if len(res) == 0 || res[0].Name != "permissions" {
		t.Fatalf("Search(sandbox) = %+v, want permissions", res)
	}
	res = ix.Search("re", 10)
	if len(res) == 0 || res[0].Name != "resume" {
		t.Fatalf("Search(re) = %+v, want resume", res)
	}
}
