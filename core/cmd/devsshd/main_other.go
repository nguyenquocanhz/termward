//go:build !linux

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "devsshd runs on Linux only (build it with GOOS=linux and start it inside WSL or a VM).")
	os.Exit(1)
}
