package catalog_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/relux-works/curator-model-router/pkg/recommend"
)

func TestNotesDescribeShippedTiers(t *testing.T) {
	data, err := os.ReadFile("NOTES.md")
	if err != nil {
		t.Fatal(err)
	}
	notes := string(data)
	if strings.Contains(notes, ".temp/rc/") {
		t.Fatal("worktree-specific path in notes")
	}
	catalog, err := recommend.LoadCatalog(recommend.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	policy := recommend.DefaultPolicy()
	for _, edge := range []float64{policy.TierEdges.S, policy.TierEdges.A, policy.TierEdges.B} {
		if !strings.Contains(notes, fmt.Sprintf("≥%g", edge)) {
			t.Fatal("missing shipped edge", edge)
		}
	}
	if !strings.Contains(notes, "quality_from_bughunt_fallback") || !strings.Contains(notes, "Huryn, P.") {
		t.Fatal("missing source/attribution")
	}
	for _, row := range catalog.Rows {
		if row.Quality.Coding != nil || row.Quality.Overall != nil {
			t.Fatal("embedded index")
		}
		if row.Quality.Review != nil && !strings.Contains(notes, "| `"+row.Runtime+"/"+row.Model+"/"+row.Effort+"` |") {
			t.Fatal("missing measured row", row.Candidate)
		}
	}
}
