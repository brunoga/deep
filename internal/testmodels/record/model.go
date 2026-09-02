// Package record holds a document shaped like one a handful of independent
// services would share: a row in a store, held as a serialized blob, whose
// fields are each owned by a different writer.
//
// It exists for the contention benchmark. Whole-payload compare-and-swap
// conflicts whenever any field changes; a conditional patch conflicts only when
// the field its condition names changes. The fields here are deliberately
// independent so the difference between those two is what the benchmark
// measures.
package record

//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=Record -output record_deep.go .

// Record is an inventory row. Version is what a compare-and-swap writer checks;
// the rest are owned one apiece by the services that update them.
type Record struct {
	ID      string `json:"id"`
	Version int    `json:"version"`

	Stock    int `json:"stock"`    // inventory service
	Price    int `json:"price"`    // pricing service, in cents
	Views    int `json:"views"`    // analytics
	Rating   int `json:"rating"`   // reviews
	Reserved int `json:"reserved"` // checkout
	Shipped  int `json:"shipped"`  // fulfilment
	Returned int `json:"returned"` // returns
	Flagged  int `json:"flagged"`  // moderation
}

// Fields lists the JSON Pointer path of every service-owned counter, in the
// order the benchmark hands them out to writers.
var Fields = []string{
	"/stock", "/price", "/views", "/rating",
	"/reserved", "/shipped", "/returned", "/flagged",
}

// Get returns the counter at one of the paths in Fields.
func (r *Record) Get(path string) int {
	switch path {
	case "/stock":
		return r.Stock
	case "/price":
		return r.Price
	case "/views":
		return r.Views
	case "/rating":
		return r.Rating
	case "/reserved":
		return r.Reserved
	case "/shipped":
		return r.Shipped
	case "/returned":
		return r.Returned
	case "/flagged":
		return r.Flagged
	}
	return 0
}

// Set writes the counter at one of the paths in Fields.
func (r *Record) Set(path string, v int) {
	switch path {
	case "/stock":
		r.Stock = v
	case "/price":
		r.Price = v
	case "/views":
		r.Views = v
	case "/rating":
		r.Rating = v
	case "/reserved":
		r.Reserved = v
	case "/shipped":
		r.Shipped = v
	case "/returned":
		r.Returned = v
	case "/flagged":
		r.Flagged = v
	}
}

// Total sums every counter. The benchmark checks it against the number of
// successful writes: a lost update shows up as a shortfall.
func (r *Record) Total() int {
	return r.Stock + r.Price + r.Views + r.Rating +
		r.Reserved + r.Shipped + r.Returned + r.Flagged
}
