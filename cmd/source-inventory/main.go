package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/libteca/libteca/internal/sourceinventory"
)

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("source-inventory", flag.ContinueOnError)
	flags.SetOutput(stderr)
	snapshot := flags.String("snapshot", "", "consistent standalone disposable database snapshot; never a live database")
	rootMap := flags.String("root-map", "", "optional JSON array mapping library_id to an absolute disposable_root for metadata-only checks")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *snapshot == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "--snapshot is required and positional arguments are not accepted")
		return 2
	}
	mappings := []sourceinventory.RootMapping{}
	if *rootMap != "" {
		f, err := os.Open(*rootMap)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer f.Close()
		mappings, err = decodeRootMappings(f)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	report, err := sourceinventory.Read(ctx, *snapshot, mappings)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func decodeRootMappings(input io.Reader) ([]sourceinventory.RootMapping, error) {
	decoder := json.NewDecoder(input)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("root map must be a JSON array")
	}
	mappings := []sourceinventory.RootMapping{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil || token != json.Delim('{') {
			return nil, fmt.Errorf("each root mapping must be a JSON object")
		}
		mapping := sourceinventory.RootMapping{}
		seen := map[string]bool{}
		for decoder.More() {
			token, err = decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return nil, fmt.Errorf("duplicate or invalid root mapping key: %v", token)
			}
			seen[key] = true
			switch key {
			case "library_id":
				err = decoder.Decode(&mapping.LibraryID)
			case "disposable_root":
				err = decoder.Decode(&mapping.DisposableRoot)
			default:
				return nil, fmt.Errorf("unknown root mapping key: %s", key)
			}
			if err != nil {
				return nil, err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return nil, err
		}
		if !seen["library_id"] || !seen["disposable_root"] || mapping.LibraryID <= 0 || mapping.DisposableRoot == "" {
			return nil, fmt.Errorf("each root mapping requires positive library_id and nonempty disposable_root")
		}
		mappings = append(mappings, mapping)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("root map must contain exactly one JSON array")
	}
	return mappings, nil
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
