// Package evidence implements append-only evidence documents and replayable
// suitability views. All times are explicit inputs; importers do no network I/O.
//
// Bug Hunt uses the pinned skill-agents-management v0.5.40 benchdata leaf's
// compiled rows and registry facts, with a declared versioned identity mapping
// and fixed release import/retrieval metadata. Fixed denominators retain scale
// 105; only measured fixed claims have sample counts. Interpolations keep
// unknown effort, and cost claims remain non-quality list_cost observations.
// Invalid denominators return evidence_bughunt_denominator. No vendor adapter,
// agentic package, subprocess, plugin registration, or clock is used.
//
// A module pin upgrade changes raw artifact and registry identities, so new
// content addresses are expected. Stable publication origins preserve dedup.
// Existing stores require cmr evidence rebind with the new registry; old
// snapshots remain replayable using their immutable registry projections.
//
// TODO(decision): the CLI default is XDG_DATA_HOME/curator/model-router/evidence,
// with the local share directory as fallback. This keeps derived routing data
// separate from credentials and launch configuration. Store always requires an
// explicit root, and tests always pass a temporary directory.
//
// TODO(decision): require manual stale-lock recovery after confirming no writer.
// Writers use current.lock with O_EXCL and never steal a stale lock. Following a
// crash an operator must confirm that no writer is running, then remove the lock.
// Readers observe immutable files and an atomically replaced current pointer.
// Parent-directory fsync follows current's atomic rename, best effort where the
// platform or filesystem cannot sync a directory.
//
// Imports retain source resolution; status and effective model resolution are
// derived per snapshot using its bound registry. Rebind creates a new snapshot
// without rewriting imports or earlier snapshots. A source-declared unresolved
// observation remains unresolved. Notes created after a view's evaluated_at are
// excluded entirely, so a frozen view never uses future judgements.
package evidence
