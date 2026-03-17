package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/hajizar/abacus/internal/cli"
)

func main() {
	err := cli.Execute()
	if err == nil {
		return
	}

	var exitErr cli.ExitError
	if errors.As(err, &exitErr) {
		if exitErr.ShowStderr {
			fmt.Fprintln(os.Stderr, exitErr.Err)
		}
		os.Exit(exitErr.Code)
	}

	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
