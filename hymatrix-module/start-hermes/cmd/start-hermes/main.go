package main

import (
	"fmt"
	"os"

	starthermes "github.com/vertrai/hub/hymatrix-module/start-hermes"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "reset-weixin" {
		if err := starthermes.ResetWeixin(os.Args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "reset Hermes Weixin: %v\n", err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stdout, "Installing Hub Gateway skills and starting Hermes...")
	if err := starthermes.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "start Hermes: %v\n", err)
		os.Exit(1)
	}
}
