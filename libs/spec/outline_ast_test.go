package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOutlineSaysWhereEachThingIsAndHashesWhatItSays(t *testing.T) {
	d := grocery(t)
	by := map[string]OutlineItem{}
	for _, o := range Outline(d) {
		by[o.ID] = o
		if o.Path == "" || len(o.Hash) != 64 {
			t.Fatalf("%+v", o)
		}
	}
	if by["recurring-orders"].Path != "features" || by["c-3f2a"].Path != "features/recurring-orders" || by["q-auto-charge"].Path != "questions" {
		t.Fatalf("paths: %+v %+v %+v", by["recurring-orders"], by["c-3f2a"], by["q-auto-charge"])
	}
	// a change to a criterion changes its hash and its item's (which holds it), and nobody else's
	edited := mustApply(t, d, PatchOp{Op: "text", Item: "c-3f2a", Text: "A household sets Milk to repeat every week."})
	changed := 0
	for _, o := range Outline(edited) {
		if o.Hash != by[o.ID].Hash {
			changed++
			if o.ID != "c-3f2a" && o.ID != "recurring-orders" {
				t.Errorf("%s changed its hash", o.ID)
			}
		}
	}
	if changed != 2 {
		t.Fatalf("%d hashes changed", changed)
	}
	// reformatting changes nothing
	again, _ := Parse(Print(d))
	for _, o := range Outline(again) {
		if o.Hash != by[o.ID].Hash {
			t.Errorf("%s: reformatting changed the hash", o.ID)
		}
	}
	// a note section gets a readable path
	n, _ := Parse("## Ideas for later\n- **Thing** {#thing}: x\n")
	if got := Outline(n)[0].Path; got != "ideas-for-later" {
		t.Fatalf("%s", got)
	}
}

func TestTheParsedFormHoldsEveryItem(t *testing.T) {
	d := grocery(t)
	b, err := d.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Ospec    string `json:"ospec"`
		Title    string
		Settings []struct{ Key, Value string }
		Sections []struct {
			Name  string
			Kind  string
			Items []struct {
				ID, Title string
				Body      []struct {
					Type    string
					Key     string
					Entries []struct{ ID, Text string }
				}
			}
		}
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	if back.Ospec != "1" || back.Title != "Grocery Restocking" || len(back.Settings) != 4 || len(back.Sections) != 10 {
		t.Fatalf("%+v", back)
	}
	ids := 0
	for _, s := range back.Sections {
		for _, it := range s.Items {
			if it.ID != "" {
				ids++
			}
			for _, bn := range it.Body {
				for _, e := range bn.Entries {
					if e.ID != "" {
						ids++
					}
				}
			}
		}
	}
	if ids != 23 {
		t.Fatalf("%d ids in the parsed form, want 23", ids)
	}
	// stable, and never null where a list is expected
	again, _ := d.Clone().JSON()
	if string(again) != string(b) {
		t.Fatal("not stable")
	}
	empty, _ := (&Doc{}).JSON()
	if !strings.Contains(string(empty), `"sections": []`) {
		t.Fatalf("%s", empty)
	}
	// notes are kept
	n, _ := Parse("## Notes\nA free sentence.\n")
	nb, _ := n.JSON()
	if !strings.Contains(string(nb), "A free sentence.") {
		t.Fatalf("%s", nb)
	}
}
