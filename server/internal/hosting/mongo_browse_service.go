package hosting

// The server-level MongoDB document browser (MongoController::browser /
// browse / export). The per-website browser in site_mongo_browser.go is the
// same tool confined to one account's prefixed databases; this one is the
// administrator's view and so touches any database on the server. That is
// the whole difference: no prefix, no ownership assertion — the route's
// mongo.view/mongo.manage permissions are the gate, and the mutating
// operations re-check mongo.manage in the handler.
//
// Every call goes through MongoServerService.call, so successful changes are
// audited against the server exactly like the rest of this area.

import (
	"context"
	"encoding/json"
	"time"
)

// Collections are the collections of a database (mongo.collections).
func (s *MongoServerService) Collections(ctx context.Context, db string) ([]MongoCollection, error) {
	res, err := s.agent.Call(ctx, "mongo.collections", map[string]any{"db": db})
	if err != nil {
		return nil, err
	}
	var o struct {
		Collections []MongoCollection `json:"collections"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return o.Collections, nil
}

// CreateCollection creates a collection (mongo.collection_create).
func (s *MongoServerService) CreateCollection(ctx context.Context, db, collection string) error {
	_, err := s.call(ctx, "mongo.collection_create",
		map[string]any{"db": db, "collection": collection},
		map[string]any{"db": db, "collection": collection})
	return err
}

// DropCollection drops a collection (mongo.collection_drop).
func (s *MongoServerService) DropCollection(ctx context.Context, db, collection string) error {
	_, err := s.call(ctx, "mongo.collection_drop",
		map[string]any{"db": db, "collection": collection},
		map[string]any{"db": db, "collection": collection})
	return err
}

// EmptyCollection deletes every document in a collection
// (mongo.collection_empty) and returns how many went.
func (s *MongoServerService) EmptyCollection(ctx context.Context, db, collection string) (int, error) {
	res, err := s.call(ctx, "mongo.collection_empty",
		map[string]any{"db": db, "collection": collection},
		map[string]any{"db": db, "collection": collection}, 90*time.Second)
	if err != nil {
		return 0, err
	}
	var o struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return 0, err
	}
	return o.Deleted, nil
}

// RenameCollection renames a collection (mongo.collection_rename).
func (s *MongoServerService) RenameCollection(ctx context.Context, db, collection, name string) error {
	_, err := s.call(ctx, "mongo.collection_rename",
		map[string]any{"db": db, "collection": collection, "name": name},
		map[string]any{"db": db, "collection": collection, "name": name})
	return err
}

// Documents is one page of a collection (mongo.documents).
func (s *MongoServerService) Documents(ctx context.Context, db, collection, filter, sort, projection string, skip, limit int) (*MongoDocuments, error) {
	res, err := s.agent.Call(ctx, "mongo.documents", map[string]any{
		"db": db, "collection": collection, "filter": filter,
		"sort": sort, "projection": projection, "skip": skip, "limit": limit,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Documents json.RawMessage `json:"documents"`
		Total     int             `json:"total"`
		Skip      int             `json:"skip"`
		Limit     int             `json:"limit"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoDocuments{Documents: o.Documents, Total: o.Total, Skip: o.Skip, Limit: o.Limit}
	if out.Documents == nil {
		out.Documents = json.RawMessage("[]")
	}
	if out.Skip == 0 {
		out.Skip = skip
	}
	if out.Limit == 0 {
		out.Limit = limit
	}
	return out, nil
}

// DeleteDocument deletes one document by its _id (EJSON)
// (mongo.document_delete).
func (s *MongoServerService) DeleteDocument(ctx context.Context, db, collection, id string) (int, error) {
	res, err := s.call(ctx, "mongo.document_delete",
		map[string]any{"db": db, "collection": collection, "id": id},
		map[string]any{"db": db, "collection": collection})
	if err != nil {
		return 0, err
	}
	var o struct {
		Deleted int `json:"deleted"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return 0, err
	}
	return o.Deleted, nil
}

// InsertDocument inserts one document (EJSON) (mongo.document_insert).
func (s *MongoServerService) InsertDocument(ctx context.Context, db, collection, document string) (any, error) {
	res, err := s.call(ctx, "mongo.document_insert",
		map[string]any{"db": db, "collection": collection, "document": document},
		map[string]any{"db": db, "collection": collection})
	if err != nil {
		return nil, err
	}
	var o struct {
		InsertedID any `json:"inserted_id"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return o.InsertedID, nil
}

// UpdateDocument replaces a document found by its _id (mongo.document_update).
func (s *MongoServerService) UpdateDocument(ctx context.Context, db, collection, id, document string) (int, int, error) {
	res, err := s.call(ctx, "mongo.document_update",
		map[string]any{"db": db, "collection": collection, "id": id, "document": document},
		map[string]any{"db": db, "collection": collection})
	if err != nil {
		return 0, 0, err
	}
	var o struct {
		Matched  int `json:"matched"`
		Modified int `json:"modified"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return 0, 0, err
	}
	return o.Matched, o.Modified, nil
}

// Aggregate runs an aggregation pipeline (mongo.aggregate).
func (s *MongoServerService) Aggregate(ctx context.Context, db, collection, pipeline string, limit int) (*MongoAggregate, error) {
	res, err := s.agent.Call(ctx, "mongo.aggregate", map[string]any{
		"db": db, "collection": collection, "pipeline": pipeline, "limit": limit,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Documents json.RawMessage `json:"documents"`
		Ms        int             `json:"ms"`
		Limit     int             `json:"limit"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoAggregate{Documents: o.Documents, Ms: o.Ms, Limit: o.Limit}
	if out.Documents == nil {
		out.Documents = json.RawMessage("[]")
	}
	if out.Limit == 0 {
		out.Limit = limit
	}
	return out, nil
}

// Export documents as a JSON array or JSONL (mongo.export, bounded).
func (s *MongoServerService) Export(ctx context.Context, db, collection, format, filter string, limit int) (*MongoExport, error) {
	res, err := s.call(ctx, "mongo.export", map[string]any{
		"db": db, "collection": collection, "format": format, "filter": filter, "limit": limit,
	}, map[string]any{"db": db, "collection": collection, "format": format}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Content string `json:"content"`
		Count   int    `json:"count"`
		Format  string `json:"format"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoExport{Content: o.Content, Count: o.Count, Format: o.Format}
	if out.Format == "" {
		out.Format = format
	}
	return out, nil
}

// Import documents into a collection (mongo.import).
func (s *MongoServerService) Import(ctx context.Context, db, collection, format, content string) (*MongoImport, error) {
	res, err := s.call(ctx, "mongo.import", map[string]any{
		"db": db, "collection": collection, "format": format, "content": content,
	}, map[string]any{"db": db, "collection": collection, "format": format}, 120*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Inserted int `json:"inserted"`
		Errors   int `json:"errors"`
		Total    int `json:"total"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	return &MongoImport{Inserted: o.Inserted, Errors: o.Errors, Total: o.Total}, nil
}

// Schema analyses a collection over a bounded sample (mongo.schema).
func (s *MongoServerService) Schema(ctx context.Context, db, collection string, sample int) (*MongoSchema, error) {
	res, err := s.agent.Call(ctx, "mongo.schema", map[string]any{
		"db": db, "collection": collection, "sample": sample,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Sampled int             `json:"sampled"`
		Fields  json.RawMessage `json:"fields"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	out := &MongoSchema{Sampled: o.Sampled, Fields: o.Fields}
	if out.Fields == nil {
		out.Fields = json.RawMessage("[]")
	}
	return out, nil
}

// Explain explains an aggregation pipeline (mongo.explain).
func (s *MongoServerService) Explain(ctx context.Context, db, collection, pipeline string) (json.RawMessage, error) {
	res, err := s.agent.Call(ctx, "mongo.explain", map[string]any{
		"db": db, "collection": collection, "pipeline": pipeline,
	}, 90*time.Second)
	if err != nil {
		return nil, err
	}
	var o struct {
		Explain json.RawMessage `json:"explain"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Explain == nil {
		o.Explain = json.RawMessage("{}")
	}
	return o.Explain, nil
}

// Indexes are the indexes of a collection (mongo.indexes).
func (s *MongoServerService) Indexes(ctx context.Context, db, collection string) (json.RawMessage, error) {
	res, err := s.agent.Call(ctx, "mongo.indexes", map[string]any{"db": db, "collection": collection})
	if err != nil {
		return nil, err
	}
	var o struct {
		Indexes json.RawMessage `json:"indexes"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return nil, err
	}
	if o.Indexes == nil {
		o.Indexes = json.RawMessage("[]")
	}
	return o.Indexes, nil
}

// CollectionStats is the size, counts and validation rules for one
// collection (mongo.collection_stats).
func (s *MongoServerService) CollectionStats(ctx context.Context, db, collection string) (json.RawMessage, error) {
	return s.agent.Call(ctx, "mongo.collection_stats", map[string]any{"db": db, "collection": collection})
}

// CreateIndex creates an index (mongo.index_create) and returns its name.
func (s *MongoServerService) CreateIndex(ctx context.Context, db, collection, keys, options string) (string, error) {
	res, err := s.call(ctx, "mongo.index_create", map[string]any{
		"db": db, "collection": collection, "keys": keys, "options": options,
	}, map[string]any{"db": db, "collection": collection}, 300*time.Second)
	if err != nil {
		return "", err
	}
	var o struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(res, &o); err != nil {
		return "", err
	}
	return o.Name, nil
}

// DropIndex drops an index (mongo.index_drop).
func (s *MongoServerService) DropIndex(ctx context.Context, db, collection, name string) error {
	_, err := s.call(ctx, "mongo.index_drop",
		map[string]any{"db": db, "collection": collection, "name": name},
		map[string]any{"db": db, "collection": collection, "name": name})
	return err
}
