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
	case "sync":
		syncCommand(os.Args[2:])
	default:
		fmt.Println("Usage: vault-zero <command> <args>")
		fmt.Println("Commands:")
		fmt.Println("  init <root folder>")
		fmt.Println("  sync <root folder>")
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

func syncCommand(args []string) {
	err := vaultzero.Sync(context.Background(), args[0])
	if err != nil {
		fmt.Printf("Failed to sync vault-zero: %v\n", err)
		return
	}
}
