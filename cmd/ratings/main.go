// Builds a blind rating sample from eval snapshots, and analyses the user's
// ratings against the deterministic quality metrics.
//
// Usage:
//
//	go run ./cmd/ratings sample -n 150 -seed 1 -out DIR SNAPSHOT.json... [-history FILE]
//	go run ./cmd/ratings analyze -dir DIR
//	go run ./cmd/ratings history-add -dir DIR -history FILE -round N
//
// Snapshots must be annotated first: go run ./cmd/eval -rescore SNAPSHOT.json
//
// -history FILE (sample) hides names already rated in an earlier round: any
// chosen (query, domain) pair found in the history file is kept in key.json
// with its stored rating (Prefilled) but omitted from items.json, so the
// rating page never re-asks for it. analyze uses a Prefilled rating when
// ratings.json has none for that entry; ratings.json always wins. Sampling
// also never puts two TLD variants of the same name (same query, same SLD)
// in one round, with or without -history.
//
// history-add records every rated (query, domain) pair from a finished
// round's key.json/ratings.json into the history file (creating it if
// missing), so later rounds can skip repeats.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "sample":
		err = runSample(os.Args[2:])
	case "analyze":
		err = runAnalyze(os.Args[2:])
	case "history-add":
		err = runHistoryAdd(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ratings: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: ratings sample -n N -seed S -out DIR [-history FILE] SNAPSHOT...\n"+
		"       ratings analyze -dir DIR\n"+
		"       ratings history-add -dir DIR -history FILE -round N")
	os.Exit(2)
}
