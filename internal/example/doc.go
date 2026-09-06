// Package example implements Sapien's saved-example domain (PLAN.md §34b):
// the "<id>.example.yaml" file format, where an example's file lives
// depending on its scope, and the SQLite index that makes examples
// queryable for the CLI, MCP, and the flow/context-builder surfaces.
//
// An example is a domain.SavedExample: a saved, reusable request for exactly
// one operation. Files are the source of truth, one per example; the SQLite
// index (the "examples" table) holds just enough metadata (operation,
// service, scope, description, tags, whether it is verified, its verified
// environment, and its file path) to filter and order without reading every
// file, and is rebuildable from the files at any time (see Store.Reindex).
// See Store for the index/CRUD surface and Locator for where an example's
// file lives.
package example
