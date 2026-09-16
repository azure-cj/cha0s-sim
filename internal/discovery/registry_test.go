package discovery

import (
	"sync"
	"testing"
)

func TestRecordCreatesNewEndpoint(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/api/users")

	list := r.List("chaos")
	if len(list) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(list))
	}
	e := list[0]
	if e.Method != "GET" {
		t.Errorf("Method = %q, want %q", e.Method, "GET")
	}
	if e.Path != "/api/users" {
		t.Errorf("Path = %q, want %q", e.Path, "/api/users")
	}
	if e.SeenCount != 1 {
		t.Errorf("SeenCount = %d, want 1", e.SeenCount)
	}
	if e.SessionName != "chaos" {
		t.Errorf("SessionName = %q, want %q", e.SessionName, "chaos")
	}
	if e.LastSeen.IsZero() {
		t.Error("LastSeen is zero, want a real timestamp")
	}
}

func TestRecordIncrementsExistingEndpointNotDuplicate(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/api/users")
	r.Record("chaos", "GET", "/api/users")
	r.Record("chaos", "GET", "/api/users")

	list := r.List("chaos")
	if len(list) != 1 {
		t.Fatalf("got %d endpoints, want 1 (repeated path must not duplicate)", len(list))
	}
	if list[0].SeenCount != 3 {
		t.Errorf("SeenCount = %d, want 3", list[0].SeenCount)
	}
}

func TestNumericSegmentsNormalizeToIDPlaceholder(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/api/users/42")
	r.Record("chaos", "GET", "/api/users/7")

	list := r.List("chaos")
	if len(list) != 1 {
		t.Fatalf("got %d endpoints, want 1 (numeric segments must collapse)", len(list))
	}
	e := list[0]
	if e.Path != "/api/users/{id}" {
		t.Errorf("Path = %q, want %q", e.Path, "/api/users/{id}")
	}
	if e.SeenCount != 2 {
		t.Errorf("SeenCount = %d, want 2", e.SeenCount)
	}
}

func TestNonNumericSegmentsPreserved(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/api/v2/users/42")
	r.Record("chaos", "GET", "/api/v2/users/007")

	list := r.List("chaos")
	if len(list) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(list))
	}
	if list[0].Path != "/api/v2/users/{id}" {
		t.Errorf("Path = %q, want %q (only numeric segments collapse)", list[0].Path, "/api/v2/users/{id}")
	}
}

func TestDSLLikeAndMixedSegmentsStayLiteral(t *testing.T) {
	r := NewRegistry()
	// hex, slugs, and mixed token segments must NOT be normalized
	for _, p := range []string{"/api/users/abc123", "/api/users/me", "/users/u1"} {
		r.Record("chaos", "GET", p)
	}
	list := r.List("chaos")
	if len(list) != 3 {
		t.Fatalf("got %d endpoints, want 3 (non-numeric segments stay distinct)", len(list))
	}
}

func TestListSortedBySeenCountDescending(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/api/rare")
	r.Record("chaos", "GET", "/api/hot")
	r.Record("chaos", "GET", "/api/hot")
	r.Record("chaos", "GET", "/api/hot")
	r.Record("chaos", "GET", "/api/warm")
	r.Record("chaos", "GET", "/api/warm")

	list := r.List("chaos")
	if len(list) != 3 {
		t.Fatalf("got %d endpoints, want 3", len(list))
	}
	want := []int{3, 2, 1}
	for i, w := range want {
		if list[i].SeenCount != w {
			t.Fatalf("list[%d].SeenCount = %d, want %d (must be sorted descending)", i, list[i].SeenCount, w)
		}
	}
	if list[0].Path != "/api/hot" {
		t.Errorf("most-seen endpoint = %q, want %q", list[0].Path, "/api/hot")
	}
}

func TestListEmptyWhenNothingObserved(t *testing.T) {
	r := NewRegistry()
	if got := r.List("chaos"); len(got) != 0 {
		t.Errorf("empty registry List returned %d endpoints, want 0", len(got))
	}
}

func TestClearRemovesOnlyTargetSession(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/api/a")
	r.Record("chaos", "GET", "/api/a/1")
	r.Record("security", "GET", "/api/b")
	r.Record("security", "GET", "/api/c")

	r.Clear("chaos")

	if got := r.List("chaos"); len(got) != 0 {
		t.Errorf("chaos list after Clear = %d endpoints, want 0", len(got))
	}
	sec := r.List("security")
	if len(sec) != 2 {
		t.Fatalf("security list after clearing chaos = %d endpoints, want 2 (clear must be session-scoped)", len(sec))
	}
}

func TestSameRouteOnDifferentSessionsDoNotCollide(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/api/users")
	r.Record("security", "GET", "/api/users")

	if got := r.List("chaos"); len(got) != 1 {
		t.Errorf("chaos list = %d endpoints, want 1", len(got))
	}
	if got := r.List("security"); len(got) != 1 {
		t.Errorf("security list = %d endpoints, want 1", len(got))
	}
	if got := r.List("chaos"); got[0].SessionName != "chaos" {
		t.Errorf("SessionName = %q, want %q", got[0].SessionName, "chaos")
	}
	if got := r.List("security"); got[0].SessionName != "security" {
		t.Errorf("SessionName = %q, want %q", got[0].SessionName, "security")
	}
}

func TestRootPathSurvivesNormalization(t *testing.T) {
	r := NewRegistry()
	r.Record("chaos", "GET", "/")

	list := r.List("chaos")
	if len(list) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(list))
	}
	if list[0].Path != "/" {
		t.Errorf("Path = %q, want %q (root path must not collapse to empty)", list[0].Path, "/")
	}
}

func TestConcurrentRecordsNoRace(t *testing.T) {
	r := NewRegistry()

	const workers = 16
	const hits = 100

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < hits; i++ {
				r.Record("chaos", "GET", "/api/users/1")
				r.Record("chaos", "POST", "/api/items")
				r.Record("security", "GET", "/health")
			}
		}()
	}
	wg.Wait()

	// All three recorded routes must have collapsed/normalized correctly and
	// every one of workers*hits observations must be accounted for.
	chaos := r.List("chaos")
	if len(chaos) != 2 {
		t.Fatalf("chaos list = %d endpoints, want 2 (/api/users/{id}, /api/items)", len(chaos))
	}
	counts := map[string]int{}
	for _, e := range chaos {
		counts[e.Path] = e.SeenCount
	}
	if got := counts["/api/users/{id}"]; got != workers*hits {
		t.Errorf("/api/users/{id} SeenCount = %d, want %d", got, workers*hits)
	}
	if got := counts["/api/items"]; got != workers*hits {
		t.Errorf("/api/items SeenCount = %d, want %d", got, workers*hits)
	}

	sec := r.List("security")
	if len(sec) != 1 || sec[0].Path != "/health" {
		t.Fatalf("security list = %+v, want exactly /health", sec)
	}
	if sec[0].SeenCount != workers*hits {
		t.Errorf("/health SeenCount = %d, want %d", sec[0].SeenCount, workers*hits)
	}
}
