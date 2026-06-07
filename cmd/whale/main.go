package main

import (
	"fmt"
	"os"

	"github.com/usewhale/whale/internal/execboundary"
	"github.com/usewhale/whale/internal/execenv"
	"github.com/usewhale/whale/internal/ui/cli/cmd"
)

func main() {
	if os.Getenv(execenv.WrapperModeEnv) == "1" {
		os.Exit(execboundary.RunWrapper(os.Args[1:], os.Stdout, os.Stderr))
	}
	if err := cmd.Execute(); err != nil {
		if code, ok := cmd.ExitCode(err); ok {
			os.Exit(code)
		}
		fmt.Printf("error: %v\n", err)
		os.Exit(1)
	}
}
