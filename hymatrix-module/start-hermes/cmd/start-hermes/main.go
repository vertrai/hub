package main

import (
	"fmt"
	"os"

	starthermes "github.com/vertrai/hub/hymatrix-module/start-hermes"
)

func main() {
	fmt.Fprintln(os.Stdout, "Installing Hub Gateway skills and starting Hermes...")
	if err := starthermes.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "start Hermes: %v\n", err)
		os.Exit(1)
	}
}
