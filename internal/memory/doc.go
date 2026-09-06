// Package memory implements Sapien's memory domain: the Markdown+front-matter
// file format (PLAN.md §10), where a memory lives depending on its scope
// (§12), the SQLite index that makes memories queryable, and structural-first
// retrieval and ranking (§13).
//
// A memory is a domain.Memory. Non-personal memories (workspace, service,
// flow) are canonically stored as one Markdown file per memory, with the
// engine's SQLite index rebuildable from those files at any time; personal
// memories live only in SQLite (file_path is NULL). See Store for the
// index/CRUD surface and Locator for where a memory's file lives.
package memory
