package main

import (
	_ "time/tzdata"

	"github.com/anupcshan/anantha/cmd/anantha/cmd"
)

func main() {
	cmd.Execute()
}
