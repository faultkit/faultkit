package fixtures

// stale-results: a successful vector query whose matches point at documents
// that are meant not to exist — deleted from the source corpus, or never
// indexed. The ids are faultkit-prefixed (Pinecone) or fixed UUIDs (Qdrant),
// chosen to be unlikely in a real index, but nothing guarantees their absence:
// the test owner must make sure their store has no record with these ids. The
// fixture only injects the response. Whether the application looks the ids up,
// rejects them, falls back, or cites them is for the target's own assertions
// to check — and the inline metadata/payload may let it skip any lookup
// entirely. A fired fault proves none of deletion propagation, authorization
// isolation, retry safety, or a general RAG invariant. The metadata/payload
// text says it is stale, so a user reading a transcript can see where it came
// from.
//
// The bodies follow each vendor's documented response schema; the request
// body is never read, so the namespace and result count are fixed.
const (
	// pineconeStaleResults is a 200 from the Pinecone data-plane `POST /query`
	// (QueryResponse: matches[] of ScoredVector{id, score, values, metadata},
	// namespace, usage.readUnits; plus the deprecated, empty `results` the
	// REST API still returns). "" is the default namespace, as in the
	// describe_index_stats example of the same API reference.
	pineconeStaleResults = `{"results":[],"matches":[{"id":"faultkit-stale-doc-1","score":0.91,"values":[],"metadata":{"source":"faultkit-stale-doc-1.md","text":"faultkit stale-results: this document was deleted from the corpus, but the index still returns it."}},{"id":"faultkit-stale-doc-2","score":0.87,"values":[],"metadata":{"source":"faultkit-stale-doc-2.md","text":"faultkit stale-results: this document was deleted from the corpus, but the index still returns it."}}],"namespace":"","usage":{"readUnits":5}}`

	// qdrantStaleResults is a 200 from Qdrant's Query API
	// `POST /collections/{name}/points/query`: {result: QueryResponse{points:
	// []ScoredPoint}, status, time}. ScoredPoint requires id, version and
	// score; the ids are valid version-4 UUIDs (an ExtendedPointId is a
	// uint64 or a UUID string).
	qdrantStaleResults = `{"result":{"points":[{"id":"00000000-0000-4000-8000-00000000fa01","version":7,"score":0.91,"payload":{"source":"faultkit-stale-doc-1.md","text":"faultkit stale-results: this document was deleted from the corpus, but the index still returns it."}},{"id":"00000000-0000-4000-8000-00000000fa02","version":7,"score":0.87,"payload":{"source":"faultkit-stale-doc-2.md","text":"faultkit stale-results: this document was deleted from the corpus, but the index still returns it."}}]},"status":"ok","time":0.002}`
)
