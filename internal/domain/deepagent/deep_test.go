package deepagent

import "testing"

func TestExpandAndLooksDeep(t *testing.T) {
	ph := Expand("add code_search")
	if len(ph) != 3 {
		t.Fatalf("phases=%d", len(ph))
	}
	if ph[0].ID != "plan" || ph[1].ID != "act" || ph[2].ID != "reflect" {
		t.Fatalf("%+v", ph)
	}
	if !LooksDeep("/deep fix bug") {
		t.Fatal("looks")
	}
	if StripPrefix("/deep fix bug") != "fix bug" {
		t.Fatal(StripPrefix("/deep fix bug"))
	}
	if ComparisonDoc() == "" {
		t.Fatal("empty doc")
	}
}
