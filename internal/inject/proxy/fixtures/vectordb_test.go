package fixtures_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/faultkit/faultkit/internal/inject/proxy/fixtures"
)

// decodeStrict unmarshals body into v, rejecting keys the vendor schema
// doesn't define, so a misspelled optional field (e.g. metdata for metadata)
// fails the test instead of being silently ignored.
func decodeStrict(t *testing.T, body string, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("body does not match the vendor schema: %v\nbody=%s", err, body)
	}
}

func staleFixture(t *testing.T, provider, wantPath string) fixtures.Fixture {
	t.Helper()
	f, ok := fixtures.For("stale-results", provider)
	if !ok {
		t.Fatalf("expected a fixture for (stale-results, %s)", provider)
	}
	if f.Status != 200 {
		t.Errorf("Status = %d, want 200", f.Status)
	}
	if f.Path != wantPath {
		t.Errorf("Path = %q, want %q", f.Path, wantPath)
	}
	return f
}

// Pinecone data-plane QueryResponse: matches[] of ScoredVector (id required,
// 1-512 chars), namespace, usage.readUnits, deprecated results[].
func TestStaleResultsPineconeQueryResponse(t *testing.T) {
	f := staleFixture(t, "pinecone", "/query")
	var resp struct {
		Results []json.RawMessage `json:"results"`
		Matches []struct {
			ID           string          `json:"id"`
			Score        *float64        `json:"score"`
			Values       []float64       `json:"values"`
			SparseValues json.RawMessage `json:"sparseValues"`
			Metadata     map[string]any  `json:"metadata"`
		} `json:"matches"`
		Namespace *string `json:"namespace"`
		Usage     struct {
			ReadUnits *int64 `json:"readUnits"`
		} `json:"usage"`
	}
	decodeStrict(t, f.Body, &resp)

	if len(resp.Matches) == 0 {
		t.Fatal("matches must be non-empty: stale results are matches, not an empty result")
	}
	if resp.Namespace == nil {
		t.Error("namespace missing")
	}
	if resp.Usage.ReadUnits == nil {
		t.Error("usage.readUnits missing")
	}
	seen := map[string]bool{}
	prev := 2.0
	for i, m := range resp.Matches {
		if m.ID == "" || len(m.ID) > 512 {
			t.Errorf("match %d: id %q must be 1-512 chars", i, m.ID)
		}
		if !strings.HasPrefix(m.ID, "faultkit-stale-") {
			t.Errorf("match %d: id %q must be faultkit-prefixed, to keep it unlikely to name a real record", i, m.ID)
		}
		if seen[m.ID] {
			t.Errorf("match %d: duplicate id %q", i, m.ID)
		}
		seen[m.ID] = true
		if m.Score == nil {
			t.Errorf("match %d: score missing", i)
			continue
		}
		if *m.Score > prev {
			t.Errorf("match %d: score %v > previous %v; matches are ordered most similar first", i, *m.Score, prev)
		}
		prev = *m.Score
	}
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Qdrant Query API response: {result: {points: [ScoredPoint]}, status, time,
// usage}; ScoredPoint requires id (uint64 or UUID), version and score.
func TestStaleResultsQdrantQueryResponse(t *testing.T) {
	f := staleFixture(t, "qdrant", "/collections/*/points/query")
	var resp struct {
		Usage  json.RawMessage `json:"usage"`
		Time   *float64        `json:"time"`
		Status string          `json:"status"`
		Result *struct {
			Points []struct {
				ID         json.RawMessage `json:"id"`
				Version    *uint64         `json:"version"`
				Score      *float64        `json:"score"`
				Payload    map[string]any  `json:"payload"`
				Vector     json.RawMessage `json:"vector"`
				ShardKey   json.RawMessage `json:"shard_key"`
				OrderValue json.RawMessage `json:"order_value"`
			} `json:"points"`
		} `json:"result"`
	}
	decodeStrict(t, f.Body, &resp)

	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
	if resp.Time == nil {
		t.Error("time missing")
	}
	if resp.Result == nil || len(resp.Result.Points) == 0 {
		t.Fatal("result.points must be non-empty: stale results are points, not an empty result")
	}
	seen := map[string]bool{}
	for i, p := range resp.Result.Points {
		var id string
		if err := json.Unmarshal(p.ID, &id); err != nil {
			t.Errorf("point %d: id %s must be a UUID string: %v", i, p.ID, err)
		} else if !uuidV4.MatchString(id) {
			t.Errorf("point %d: id %q is not a canonical version-4 UUID", i, id)
		}
		if seen[id] {
			t.Errorf("point %d: duplicate id %q", i, id)
		}
		seen[id] = true
		if p.Version == nil {
			t.Errorf("point %d: version missing (required)", i)
		}
		if p.Score == nil {
			t.Errorf("point %d: score missing (required)", i)
		}
		if len(p.Payload) == 0 {
			t.Errorf("point %d: payload should carry the stale document's fields", i)
		}
	}
}
