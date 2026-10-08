package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/libteca/libteca/internal/store"
	"io"
	"os"
	"path/filepath"
)

func runSourceRepair(args []string) {
	flags := flag.NewFlagSet("source-repair", flag.ExitOnError)
	data := flags.String("data", "./data", "data directory")
	mapping := flags.String("map", "", "reviewed version 1 source mapping")
	apply := flags.Bool("apply", false, "apply validated mapping")
	flags.Parse(args)
	if *mapping == "" {
		fatal(fmt.Errorf("source-repair requires --map"))
	}
	f, err := os.Open(*mapping)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	var plan store.SourceRepair
	decoder := json.NewDecoder(io.LimitReader(f, 16<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		fatal(fmt.Errorf("invalid source mapping"))
	}
	abs, err := filepath.Abs(*data)
	if err != nil {
		fatal(err)
	}
	release, err := lockDataDir(abs)
	if err != nil {
		fatal(err)
	}
	defer release()
	db, err := store.Open(filepath.Join(abs, "libteca.db"))
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	if err := db.RepairSources(plan, *apply); err != nil {
		fatal(err)
	}
	fmt.Printf("source mapping validated: %d files; applied=%t\n", len(plan.Files), *apply)
}
