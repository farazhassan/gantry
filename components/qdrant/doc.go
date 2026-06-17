// Package qdrant provides a standard-library REST client for the Qdrant vector
// database (Store: ensure-collection / upsert / search) and a Retriever adapter
// that embeds a query and returns the nearest stored documents as
// []gantry.Document. No Qdrant SDK is used.
//
// Qdrant point IDs must be unsigned integers or UUIDs; this package uses
// uint64 IDs.
package qdrant
