package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	schemaPath := flag.String("schema", "", "schema file to emit")
	markerPath := flag.String("marker", "", "optional execution marker")
	flag.Parse()

	if *schemaPath == "" {
		fmt.Fprintln(os.Stderr, "schema path is required")
		os.Exit(2)
	}
	if *markerPath != "" {
		if err := os.WriteFile(*markerPath, []byte("executed\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, "write marker:", err)
			os.Exit(2)
		}
	}
	schema, err := os.ReadFile(*schemaPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read schema:", err)
		os.Exit(2)
	}
	if _, err := os.Stdout.Write(schema); err != nil {
		fmt.Fprintln(os.Stderr, "write schema:", err)
		os.Exit(2)
	}
}
