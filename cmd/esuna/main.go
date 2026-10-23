package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"esuna/internal/jev"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "install-hook" {
		fmt.Fprintln(stderr, "not implemented")
		return 1
	}

	flags := flag.NewFlagSet("esuna", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Bool("json", false, "output JSON")
	flags.Bool("deep", false, "include repo-wide context")
	flags.Bool("with-tests", false, "include tests")
	flags.Bool("verify", false, "verify findings")
	flags.Bool("merge", false, "merge reviewer findings")
	flags.Bool("chain", false, "chain reviewers")
	flags.Bool("gate", false, "block on confident P0 findings")
	flags.String("author-model", "", "model that authored the change")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected argument: %s\n", flags.Arg(0))
		return 2
	}

	if _, err := jev.NewClient(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	// Downstream cards wire the review pipeline here.
	return 0
}
