package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: ps-extra <sync|serve> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sync":
		if err := runSync(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "sync error:", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "serve error:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}

func runSync(args []string) error {
	fmt.Println("sync: not implemented yet")
	return nil
}

func runServe(args []string) error {
	fmt.Println("serve: not implemented yet")
	return nil
}
