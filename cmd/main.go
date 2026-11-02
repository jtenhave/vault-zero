package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jtenhave/vaultzero"
)

func main() {

	switch os.Args[1] {
	case "init":
		initCommand(os.Args[2:])
	case "watch":
		watchCommand(os.Args[2:])
	default:
		fmt.Println("Usage: vault-zero <command> <args>")
		fmt.Println("Commands:")
		fmt.Println("  init <root folder>")
		fmt.Println("  watch <root folder>")
		return
	}
}

func initCommand(args []string) {
	err := vaultzero.Init(context.Background(), args)
	if err != nil {
		fmt.Printf("Failed to initialize vault-zero: %v\n", err)
		return
	}
}

func watchCommand(args []string) {
	err := vaultzero.Watch(context.Background(), args)
	if err != nil {
		fmt.Printf("Failed to watch vault-zero: %v\n", err)
		return
	}
}
