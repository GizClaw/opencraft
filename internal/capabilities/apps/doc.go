// Package apps is the application platform's registry and policy: the
// installed applications themselves (a manifest, a content root, the
// deployment layers inside it), the decisions the host makes before one
// of their layers is ever assembled, and the document-level preflight
// that runs before an install copies anything and again before an
// enabled application is assembled.
//
// The policy has two halves. whitelist.go owns the static half — which
// resource kinds an application layer may declare — and validate.go the
// dynamic one: the keys the host reserves, the {file:} references that
// must stay inside the content root, and the checks that need the merged
// document. The store (store.go) is what the rest of the host reads:
// <appHome>/apps/<id>/content is what was installed, <dataDir>/apps/<id>
// is the state root the runtime may write, and the two trees are kept
// apart on purpose — installing or updating an application must never
// overwrite the sessions and the private workspace it already has.
//
// What is not here yet, and is deliberately not half-built: the import
// adapter that normalizes a foreign flowcraft document (dropping the
// keys the contract layer provides, the provider declarations and the
// restricted kinds, and reporting every removal), and the zip/update/
// rollback paths the plugin registry has. Both read the tables and the
// preflight in this package rather than re-deciding what an application
// may contain.
package apps
