//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=Playlist -output playlist_deep.go .

package main

import (
	"fmt"
	"log"

	"github.com/brunoga/deep/v5"
)

// At extends a slice-field selector to a specific element, so a patch can
// target one position instead of the whole slice.
//
// Positional paths are the right tool when the index is meaningful (a queue
// position, a fixed-size row). When elements have their own identity, tag the
// identifying field with deep:"key" and address them by key instead — indices
// shift as the slice changes, keys do not. See the keyed_inventory example.
type Playlist struct {
	Name   string   `json:"name"`
	Tracks []string `json:"tracks"`
}

func main() {
	pl := Playlist{
		Name:   "Focus",
		Tracks: []string{"Intro", "Deep Work", "Outro"},
	}

	tracksPath := deep.Field(func(p *Playlist) *[]string { return &p.Tracks })

	fmt.Println("--- BEFORE ---")
	fmt.Printf("%v\n", pl.Tracks)

	// Replace one element by position; append by targeting the end index.
	edits := deep.Edit(&pl).
		With(deep.Set(deep.At(tracksPath, 1), "Deeper Work")).
		With(deep.Add(deep.At(tracksPath, len(pl.Tracks)), "Encore")).
		Build()

	fmt.Println("\n--- POSITIONAL EDITS ---")
	fmt.Println(edits)
	if err := deep.Apply(&pl, edits); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%v\n", pl.Tracks)

	// Remove by position.
	drop := deep.Edit(&pl).With(deep.Remove(deep.At(tracksPath, 0))).Build()

	fmt.Println("\n--- REMOVE /tracks/0 ---")
	if err := deep.Apply(&pl, drop); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%v\n", pl.Tracks)

	// How Diff describes a slice change depends on whether positions stay put.
	// Same length: each changed position is its own operation.
	shuffled := pl
	shuffled.Tracks = []string{"Encore", "Deeper Work", "Outro"}
	inPlace, err := deep.Diff(pl, shuffled)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n--- DIFF, SAME LENGTH ---")
	fmt.Println(inPlace)

	// Length changed: indices after the edit shift, so independent per-index
	// operations could not be applied safely. Diff emits one whole-slice
	// replace instead.
	grown := pl
	grown.Tracks = []string{"Deeper Work", "Interlude", "Outro", "Encore"}
	structural, err := deep.Diff(pl, grown)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n--- DIFF, INSERTED ELEMENT ---")
	fmt.Println(structural)

	if err := deep.Apply(&pl, structural); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\n%v (matches target: %v)\n", pl.Tracks, deep.Equal(pl, grown))
}
