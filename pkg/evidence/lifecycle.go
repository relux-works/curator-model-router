package evidence

import (
	"encoding/json"
	"github.com/relux-works/curator-model-router/pkg/canonical"
)

type Record struct {
	ID          string       `json:"id"`
	Status      string       `json:"status"`
	Observation *Observation `json:"observation,omitempty"`
	Note        *Note        `json:"note,omitempty"`
	Retraction  *Retraction  `json:"retraction,omitempty"`
}
type ActiveSet struct {
	Records []Record `json:"records"`
	Issues  []Issue  `json:"issues"`
}

// Resolve assumes imports have passed address validation. Status and conflicts
// are recomputed from the union; no insertion order is part of the algorithm.
func Resolve(imports []Import) (ActiveSet, error) {
	result := ActiveSet{Records: []Record{}, Issues: []Issue{}}
	records := map[string]Record{}
	edges := map[string][]string{}
	withdrawn := map[string]bool{}
	add := func(rec Record, supersedes []string) error {
		if old, ok := records[rec.ID]; ok {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(rec)
			if string(a) != string(b) {
				return refuse("evidence_record_conflict", "same address has different content")
			}
			return nil
		}
		records[rec.ID] = rec
		edges[rec.ID] = supersedes
		return nil
	}
	for _, v := range imports {
		for _, o := range v.Observations {
			if err := add(Record{ID: o.ID, Observation: &o}, o.Supersedes); err != nil {
				return result, err
			}
		}
		for _, n := range v.Notes {
			if err := add(Record{ID: n.ID, Note: &n}, n.Supersedes); err != nil {
				return result, err
			}
		}
		for _, t := range v.Retractions {
			if !validAddress(t.Target) || len(t.Target) >= 4 && t.Target[:4] == "ret:" {
				return result, refuse("evidence_invalid_target", "cannot retract retractions")
			}
			if err := add(Record{ID: t.ID, Retraction: &t}, nil); err != nil {
				return result, err
			}
			withdrawn[t.Target] = true
		}
	}
	keys := make([]string, 0, len(records))
	for k := range records {
		keys = append(keys, k)
	}
	canonical.SortByKey(keys, func(s string) string { return s })
	color := map[string]int{}
	suppressed := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if withdrawn[id] || color[id] == 2 {
			return nil
		}
		if color[id] == 1 {
			return refuse("evidence_cycle", "supersession graph contains a cycle")
		}
		color[id] = 1
		for _, target := range edges[id] {
			if !withdrawn[target] {
				suppressed[target] = true
				if err := visit(target); err != nil {
					return err
				}
			}
		}
		color[id] = 2
		return nil
	}
	for _, id := range keys {
		if err := visit(id); err != nil {
			return result, err
		}
	}
	for _, id := range keys {
		r := records[id]
		r.Status = "active"
		if withdrawn[id] {
			r.Status = "retracted"
		} else if suppressed[id] {
			r.Status = "superseded"
		}
		result.Records = append(result.Records, r)
	}
	incoming := map[string][]string{}
	for _, r := range result.Records {
		if r.Status == "active" {
			for _, t := range edges[r.ID] {
				incoming[t] = append(incoming[t], r.ID)
			}
		}
	}
	targets := make([]string, 0, len(incoming))
	for t := range incoming {
		targets = append(targets, t)
	}
	canonical.SortByKey(targets, func(s string) string { return s })
	for _, t := range targets {
		if len(incoming[t]) > 1 {
			result.Issues = append(result.Issues, Issue{"evidence_conflict", t, "multiple active replacements"})
		}
	}
	return result, nil
}
