package help

import "testing"

func TestFilterBindings_CaseInsensitive(t *testing.T) {
	results := FilterBindings("cmd")
	if len(results) == 0 {
		t.Fatal("expected matches for 'cmd'")
	}
	// All results should have "Cmd" in Key or "cmd" in Description.
	for _, b := range results {
		if !containsCI(b.Key, "cmd") && !containsCI(b.Description, "cmd") {
			t.Errorf("unexpected match: %q / %q", b.Key, b.Description)
		}
	}
}

func TestFilterBindings_ByDescription(t *testing.T) {
	results := FilterBindings("zoom")
	found := false
	for _, b := range results {
		if containsCI(b.Description, "zoom") {
			found = true
		}
	}
	if !found {
		t.Error("expected to find 'Zoom pane' binding")
	}
}

func TestFilterBindings_EmptyQuery(t *testing.T) {
	results := FilterBindings("")
	all := AllBindings()
	if len(results) != len(all) {
		t.Errorf("empty query returned %d results, want %d (all)", len(results), len(all))
	}
}

func TestFilterBindings_NoMatch(t *testing.T) {
	results := FilterBindings("zzzzzznonexistent")
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestFilterBindings_ByCategory(t *testing.T) {
	// "explorer" should match Description or Key containing "explorer".
	results := FilterBindings("explorer")
	if len(results) == 0 {
		t.Error("expected matches for 'explorer'")
	}
}

func TestAllBindings_NonEmpty(t *testing.T) {
	bindings := AllBindings()
	if len(bindings) == 0 {
		t.Fatal("AllBindings() returned empty")
	}
	// Every binding should have non-empty Key and Description.
	for i, b := range bindings {
		if b.Key == "" {
			t.Errorf("binding[%d] has empty Key", i)
		}
		if b.Description == "" {
			t.Errorf("binding[%d] has empty Description", i)
		}
		if b.Category == "" {
			t.Errorf("binding[%d] has empty Category", i)
		}
	}
}

func containsCI(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		ls, lsub := toLower(s), toLower(sub)
		for i := 0; i <= len(ls)-len(lsub); i++ {
			if ls[i:i+len(lsub)] == lsub {
				return true
			}
		}
		return false
	}()
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
