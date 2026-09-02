//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=Listing -output listing_deep.go .

package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/brunoga/deep/v5"
)

// A store keeps rows as serialized blobs, the way most databases end up being
// used for document-shaped data. Writes are strongly consistent: the store
// accepts a payload only if nothing has committed since the writer read.
//
// That is the standard optimistic-concurrency protocol, and its weakness is
// that the unit of conflict is the whole row. Two services updating completely
// unrelated fields still knock each other back, and under contention the
// retries pile up.
//
// A conditional patch narrows the conflict to the fields the write actually
// depends on. The store applies it inside the transaction, so the round trip
// that a retry costs mostly stops happening.
type Listing struct {
	ID    string `json:"id"`
	Title string `json:"title"` // catalog service
	Price int    `json:"price"` // pricing service, in cents
	Stock int    `json:"stock"` // inventory service
}

type store struct {
	blob    []byte
	version int
}

func newStore(l Listing) *store {
	return &store{blob: mustJSON(l), version: 1}
}

func (s *store) read() (Listing, int) {
	var l Listing
	if err := json.Unmarshal(s.blob, &l); err != nil {
		panic(err)
	}
	return l, s.version
}

var errStale = errors.New("version mismatch: the row moved under you")

// casWrite is the whole-payload write. Any commit since the reader's version
// rejects it, whatever field that commit touched.
func (s *store) casWrite(l Listing, expectedVersion int) error {
	if s.version != expectedVersion {
		return errStale
	}
	s.blob, s.version = mustJSON(l), s.version+1
	return nil
}

// patchWrite applies a conditional patch inside the transaction: load, apply,
// commit. The client sends no payload at all.
//
// What comes back is the result, not just an error. A condition that no longer
// holds is not a failure — it is the answer the condition asked for — and
// telling the two apart is the whole point: Apply alone returns nil either way,
// so a caller could not tell whether its conditional write had landed.
func (s *store) patchWrite(p deep.Patch[Listing]) (*deep.ApplyResult, error) {
	l, _ := s.read()

	res, err := deep.ApplyWithResult(&l, p,
		// The patch came from somewhere else, so it is confined to the fields
		// a listing writer is entitled to. An operation reaching anywhere else
		// voids the whole patch.
		deep.WithAllowedPaths("/title", "/price", "/stock"))
	if err != nil {
		return res, err
	}
	if !res.AllApplied() {
		return res, nil // nothing committed; the caller decides what to do
	}

	s.blob, s.version = mustJSON(l), s.version+1
	return res, nil
}

func main() {
	// ── 1. the problem ───────────────────────────────────────────────────────
	// Two services read the same row, change different fields, and write back.
	fmt.Println("== whole-payload compare-and-swap ==")
	s := newStore(Listing{ID: "sku-1", Title: "Kettle", Price: 1999, Stock: 12})

	pricing, pricingVersion := s.read()
	inventory, inventoryVersion := s.read()
	fmt.Printf("  both services read version %d\n", pricingVersion)

	inventory.Stock = 11
	if err := s.casWrite(inventory, inventoryVersion); err != nil {
		panic(err)
	}
	fmt.Println("  inventory commits /stock       -> ok, version 2")

	pricing.Price = 2499
	err := s.casWrite(pricing, pricingVersion)
	fmt.Printf("  pricing commits /price         -> %v\n", err)
	fmt.Println("  ...and it only touched /price. The row is the unit of")
	fmt.Println("     conflict, so unrelated writes collide and must retry.")

	// ── 2. the same interleaving, conditionally ──────────────────────────────
	fmt.Println("\n== conditional patches ==")
	s = newStore(Listing{ID: "sku-1", Title: "Kettle", Price: 1999, Stock: 12})

	// Each writer states what it depends on rather than what the row looked
	// like. Selectors give the path to the compiler instead of a string.
	pricePath := deep.Field(func(l *Listing) *int { return &l.Price })
	stockPath := deep.Field(func(l *Listing) *int { return &l.Stock })

	inventoryPatch := deep.Edit[Listing](nil).
		With(deep.Set(stockPath, 11).If(deep.Eq(stockPath, 12))).
		Build()
	pricingPatch := deep.Edit[Listing](nil).
		With(deep.Set(pricePath, 2499).If(deep.Eq(pricePath, 1999))).
		Build()

	report(s, "inventory", inventoryPatch)
	report(s, "pricing", pricingPatch)

	final, version := s.read()
	fmt.Printf("  both landed: price=%d stock=%d, version %d\n",
		final.Price, final.Stock, version)

	// ── 3. a real conflict is still caught ───────────────────────────────────
	// Narrower conditions must not mean weaker guarantees. Two writers on the
	// same field still conflict, because the second one's condition names the
	// value the first one changed.
	fmt.Println("\n== a genuine conflict still conflicts ==")
	stale := deep.Edit[Listing](nil).
		With(deep.Set(stockPath, 10).If(deep.Eq(stockPath, 12))). // read 12, but it is 11 now
		Build()
	report(s, "a writer working from a stale read", stale)
	fmt.Println("  the condition covered what the writer read, so the lost")
	fmt.Println("  update was caught. A condition that does not is a lost")
	fmt.Println("  update waiting to happen -- it must cover the read set,")
	fmt.Println("  not just the field being written.")

	// ── 4. per-operation reporting ───────────────────────────────────────────
	fmt.Println("\n== which operations landed ==")
	mixed := deep.Edit[Listing](nil).
		With(deep.Set(deep.Field(func(l *Listing) *string { return &l.Title }), "Electric Kettle")).
		With(deep.Set(pricePath, 2999).If(deep.Eq(pricePath, 1999))). // no longer 1999
		Build()

	res, err := s.patchWrite(mixed)
	fmt.Printf("  %v\n", err)
	fmt.Printf("  %s\n", res)
	fmt.Println("  Apply would have returned nil here: an operation whose")
	fmt.Println("  condition failed is not an error. The result is what says")
	fmt.Println("  the write did not fully land -- and because it did not,")
	fmt.Println("  this store committed nothing at all, including the title.")
	committed, _ := s.read()
	fmt.Printf("  title is still %q\n", committed.Title)

	// ── 5. patches from elsewhere are confined ───────────────────────────────
	fmt.Println("\n== a patch that reaches too far ==")
	overreach := deep.Patch[Listing]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/price", New: 1},
		{Kind: deep.OpReplace, Path: "/id", New: "sku-hijacked"},
	}}
	_, err = s.patchWrite(overreach)
	fmt.Printf("  %v\n", err)
	after, _ := s.read()
	fmt.Printf("  nothing applied: id=%q price=%d\n", after.ID, after.Price)
	fmt.Println("  the permitted operation was rejected along with the other")
	fmt.Println("  one -- a patch that reached for a field it was not entitled")
	fmt.Println("  to is not one to apply the rest of.")
}

// report sends a patch and prints what became of it.
func report(s *store, who string, p deep.Patch[Listing]) {
	res, err := s.patchWrite(p)
	switch {
	case err != nil:
		fmt.Printf("  %-34s -> %v\n", who, err)
	case res.AllApplied():
		fmt.Printf("  %-34s -> committed\n", who)
	default:
		fmt.Printf("  %-34s -> rejected, condition no longer holds (retry)\n", who)
	}
}

func mustJSON(l Listing) []byte {
	b, err := json.Marshal(l)
	if err != nil {
		panic(err)
	}
	return b
}
