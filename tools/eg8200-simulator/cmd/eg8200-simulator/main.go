package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "eg8200-simulator direct publisher has been retired; use eg8200-mqtt-publisher")
	os.Exit(2)
}
